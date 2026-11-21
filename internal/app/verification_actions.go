package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

type VerificationActionRequest struct {
	Operation                              string
	ExpectedDigest                         string
	ClaimID, EvidenceID, FindingID, Reason string
	ExpiresAt                              time.Time
	Destination                            string
	BundleDigest                           string
}
type VerificationActionResult struct {
	Session        verification.Session
	Attestation    *verification.CompletionAttestation
	Bundle         *MemoryFileBinding
	ManifestDigest string
}
type VerificationActionReview struct {
	Session      verification.Session
	Digest       string
	BundleDigest string
	ExportDigest string
	ExportSize   int64
}

func verificationSessionDigest(session verification.Session) string {
	body, _ := json.Marshal(session)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func (r *Runtime) ReviewVerificationAction(ctx context.Context, id string) (VerificationActionReview, error) {
	var review VerificationActionReview
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != r.ProjectIdentity() {
		return review, authz.ErrDenied
	}
	session, err := r.Verification().Current(ctx, id)
	if err != nil {
		return review, err
	}
	if session.Binding.ProjectID != r.ProjectIdentity() {
		return review, authz.ErrDenied
	}
	binding, err := r.Verification().BindingForRun(ctx, session.Binding.RunID)
	if err != nil {
		return review, err
	}
	if binding != session.Binding {
		return review, verification.ErrBindingMismatch
	}
	review.Session = session
	review.Digest = verificationSessionDigest(session)
	envelope, err := r.verificationEnvelope(ctx, session)
	if err != nil {
		return review, err
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return review, err
	}
	sum := sha256.Sum256(body)
	review.BundleDigest = envelope.Bundle.ManifestDigest
	review.ExportDigest = hex.EncodeToString(sum[:])
	review.ExportSize = int64(len(body))
	return review, nil
}

// verificationEnvelope exports canonical Process06 evidence metadata and its
// references. It is a real verified BundleEnvelope, never a Process05 handoff
// or a claim that external logs/tests have been rerun by exporting them.
func (r *Runtime) verificationEnvelope(ctx context.Context, session verification.Session) (verification.BundleEnvelope, error) {
	var envelope verification.BundleEnvelope
	body, err := json.Marshal(session)
	if err != nil {
		return envelope, err
	}
	// Community refuses an export holding a secret; Extreme exports it with
	// the secret removed (every payload, evidence included).
	firewall := security.NewFirewall(security.FirewallConfig{})
	if body, _, err = firewall.AdmitJSON(body); err != nil {
		return envelope, err
	}
	payloads := map[string][]byte{"verification.json": body}
	if err := addRuntimeCheckOutputs(payloads, session.RuntimeProof); err != nil {
		return envelope, err
	}
	for path, payload := range payloads {
		if path == "verification.json" {
			continue
		}
		// Exports apply the same secret admission policy to complete outputs.
		admitted, _, err := firewall.AdmitJSON(payload)
		if err != nil {
			return envelope, err
		}
		payloads[path] = admitted
	}
	for _, ev := range session.Evidence {
		if len(ev.ID) > 256 {
			return envelope, model.ErrInvalid
		}
		// Digests and provenance remain metadata. Raw provider transcripts and
		// hidden reasoning are never loaded into this export path.
		raw, err := json.Marshal(ev)
		if err != nil {
			return envelope, err
		}
		if security.RedactsSecrets {
			if raw, _, err = firewall.AdmitJSON(raw); err != nil {
				return envelope, err
			}
		}
		sum := sha256.Sum256([]byte(ev.ID))
		payloads["evidence/"+hex.EncodeToString(sum[:])+".json"] = raw
	}
	var entries []verification.BundleEntry
	for path, payload := range payloads {
		sum := sha256.Sum256(payload)
		entries = append(entries, verification.BundleEntry{Path: path, Digest: hex.EncodeToString(sum[:]), Size: int64(len(payload))})
	}
	bundle, err := verification.BuildEvidenceBundle(fmt.Sprintf("bundle-%s-%d", session.ID, session.Version), session.ID, session.Binding, entries, session.UpdatedAt)
	if err != nil {
		return envelope, err
	}
	envelope = verification.BundleEnvelope{Bundle: bundle, Payloads: payloads}
	if err = bundle.Verify(payloads, session.Binding); err != nil {
		return envelope, err
	}
	return envelope, nil
}
