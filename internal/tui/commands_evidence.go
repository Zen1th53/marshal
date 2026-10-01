package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/artifact"
	"github.com/Zen1th53/marshal/internal/model"
)

// evidenceListLimit bounds the inventory so a long-lived project cannot flood
// the transcript.
const evidenceListLimit = 50

type evidenceLink struct {
	claim       model.Claim
	ref         model.EvidenceRef
	contradicts bool
}

// evidenceLinks returns every claim link for an evidence ID or digest, in
// claim order. The old handler stopped at the first link and hid the rest.
func (h *CommandHandler) evidenceLinks(id, digest string) []evidenceLink {
	h.ws.mu.RLock()
	defer h.ws.mu.RUnlock()
	var links []evidenceLink
	match := func(ref model.EvidenceRef) bool {
		return ref.EvidenceID == id || (digest != "" && ref.Digest == digest)
	}
	for _, cl := range h.ws.state.Claims {
		for _, ref := range cl.SupportingEvidence {
			if match(ref) {
				links = append(links, evidenceLink{claim: cl, ref: ref})
			}
		}
		for _, ref := range cl.ContradictingEvidence {
			if match(ref) {
				links = append(links, evidenceLink{claim: cl, ref: ref, contradicts: true})
			}
		}
	}
	return links
}

func (h *CommandHandler) runtimeForEvidence() *app.Runtime {
	if a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority); ok && a != nil {
		return a.runtime
	}
	return nil
}

func (h *CommandHandler) handleEvidence(ctx context.Context, evidenceID string) (string, error) {
	evidenceID = strings.TrimPrefix(evidenceID, "#")
	var b strings.Builder
	var digest string
	if rt := h.runtimeForEvidence(); rt != nil {
		detail, err := rt.ArtifactDetail(ctx, evidenceID)
		switch {
		case err == nil:
			a := detail.Artifact
			digest = a.Digest
			fmt.Fprintf(&b, "Artifact %s (%s)\n", a.ID, a.Kind)
			fmt.Fprintf(&b, "  Digest:   %s\n", a.Digest)
			fmt.Fprintf(&b, "  Size:     %d bytes\n", a.Size)
			fmt.Fprintf(&b, "  Commit:   %s\n", a.SourceCommit)
			if len(a.TaskIDs) > 0 {
				fmt.Fprintf(&b, "  Tasks:    %s\n", strings.Join(a.TaskIDs, ", "))
			}
			if a.ProducerSession != "" {
				fmt.Fprintf(&b, "  Producer: %s\n", a.ProducerSession)
			}
			fmt.Fprintf(&b, "  Created:  %s\n", a.CreatedAt.Format("2006-01-02 15:04:05Z07:00"))
			fmt.Fprintf(&b, "  Payload:  %s", detail.Payload)
			switch detail.Payload {
			case artifact.PayloadVerified:
				b.WriteString(" (stored bytes re-read and match the digest)\n")
			case artifact.PayloadUnreadable:
				fmt.Fprintf(&b, " (%s); not usable as verified evidence\n", RedactContent(detail.PayloadErr, nil))
			default:
				b.WriteString("; not usable as verified evidence\n")
			}
		case errors.Is(err, model.ErrNotFound):
		default:
			return "", fmt.Errorf("read artifact %s: %w", evidenceID, err)
		}
	}

	links := h.evidenceLinks(evidenceID, digest)
	if b.Len() == 0 && len(links) == 0 {
		return fmt.Sprintf("Evidence %s: NOT FOUND as a stored artifact or in the active canonical claim set.", evidenceID), nil
	}
	if b.Len() == 0 {
		fmt.Fprintf(&b, "Evidence reference %s: reference only, no stored artifact bytes to check.\n", evidenceID)
	}
	if len(links) == 0 {
		b.WriteString("  Linked claims: none in the active claim set")
		return b.String(), nil
	}
	fmt.Fprintf(&b, "  Linked claims (%d):\n", len(links))
	for _, l := range links {
		verb := "supports"
		if l.contradicts {
			verb = "contradicts"
		}
		fmt.Fprintf(&b, "    %s Claim %s [%s]: %s\n", verb, l.claim.ID, l.claim.State, RedactContent(l.claim.NormalizedText, nil))
		fmt.Fprintf(&b, "      type %s, tool %s, digest %s, captured %s", l.ref.EvidenceType, l.ref.Tool, valueOr(l.ref.Digest, "none"), l.ref.CapturedAt.Format("2006-01-02 15:04:05Z07:00"))
		if l.ref.CommitSHA != "" {
			fmt.Fprintf(&b, ", commit %s", l.ref.CommitSHA)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (h *CommandHandler) handleEvidenceList(ctx context.Context) (string, error) {
	var b strings.Builder
	rt := h.runtimeForEvidence()
	if rt == nil {
		b.WriteString("Stored artifacts: unavailable (no runtime attached)\n")
	} else {
		artifacts, err := rt.Artifacts(ctx)
		if err != nil {
			return "", fmt.Errorf("list artifacts: %w", err)
		}
		sort.SliceStable(artifacts, func(i, j int) bool { return artifacts[i].CreatedAt.After(artifacts[j].CreatedAt) })
		fmt.Fprintf(&b, "Stored artifacts (%d", len(artifacts))
		if len(artifacts) > evidenceListLimit {
			fmt.Fprintf(&b, ", newest %d shown", evidenceListLimit)
			artifacts = artifacts[:evidenceListLimit]
		}
		b.WriteString("). Payloads are checked by /evidence <id>, not here.\n")
		for _, a := range artifacts {
			fmt.Fprintf(&b, "  %s  %-8s %10d B  %s  %s\n", a.ID, a.Kind, a.Size, shortDigest(a.Digest), a.CreatedAt.Format("2006-01-02 15:04"))
		}
	}
	h.ws.mu.RLock()
	refs := map[string]bool{}
	for _, cl := range h.ws.state.Claims {
		for _, ref := range append(append([]model.EvidenceRef{}, cl.SupportingEvidence...), cl.ContradictingEvidence...) {
			refs[ref.EvidenceID] = true
		}
	}
	claims := len(h.ws.state.Claims)
	h.ws.mu.RUnlock()
	fmt.Fprintf(&b, "Claim evidence references: %d distinct across %d active claims", len(refs), claims)
	return b.String(), nil
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
