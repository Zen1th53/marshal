package tui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/artifact"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

func evidenceClaim(id string, supporting, contradicting []model.EvidenceRef) model.Claim {
	return model.Claim{ID: id, State: model.ClaimState("VERIFIED"), NormalizedText: "claim " + id,
		SupportingEvidence: supporting, ContradictingEvidence: contradicting}
}

func TestEvidenceDetailChecksBytesAndListsEveryLink(t *testing.T) {
	st, ws, ctx := acceptanceWorkspace(t)
	layout, err := project.Discover((&CommandHandler{ws: ws}).runtimeForEvidence().ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := artifact.New(layout.Artifacts, st).Put(ctx, model.ArtifactInput{
		ProjectID: ws.projectID, Kind: "report", SourceCommit: "abc123", Data: strings.NewReader("test output")})
	if err != nil {
		t.Fatal(err)
	}
	captured := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	byDigest := model.EvidenceRef{EvidenceID: "EV-other", Digest: stored.Digest, Tool: "go test", EvidenceType: "test", CapturedAt: captured}
	byID := model.EvidenceRef{EvidenceID: stored.ID, Tool: "go vet", EvidenceType: "lint", CapturedAt: captured}
	h := &CommandHandler{ws: ws}
	ws.mu.Lock()
	ws.state.Claims = []model.Claim{
		evidenceClaim("C-1", []model.EvidenceRef{byDigest}, nil),
		evidenceClaim("C-2", nil, []model.EvidenceRef{byID}),
		evidenceClaim("C-3", nil, nil),
	}
	ws.mu.Unlock()

	out, err := h.handleEvidence(ctx, stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Artifact " + stored.ID, "Payload:  VERIFIED", "Linked claims (2)", "supports Claim C-1", "contradicts Claim C-2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "C-3") {
		t.Fatalf("unlinked claim listed:\n%s", out)
	}

	if err := os.Chmod(stored.Path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stored.Path, []byte("forged"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ = h.handleEvidence(ctx, stored.ID)
	if !strings.Contains(out, "Payload:  DIGEST_MISMATCH; not usable as verified evidence") {
		t.Fatalf("tampered artifact not reported:\n%s", out)
	}

	out, _ = h.handleEvidence(ctx, "EV-other")
	if !strings.Contains(out, "reference only, no stored artifact bytes") || !strings.Contains(out, "supports Claim C-1") {
		t.Fatalf("reference-only evidence:\n%s", out)
	}

	out, _ = h.handleEvidence(ctx, "EV-nowhere")
	if !strings.Contains(out, "NOT FOUND") {
		t.Fatalf("unknown evidence:\n%s", out)
	}

	out, err = h.handleEvidenceList(ctx)
	if err != nil || !strings.Contains(out, "Stored artifacts (1)") || !strings.Contains(out, stored.ID) ||
		!strings.Contains(out, "Claim evidence references: 2 distinct across 3 active claims") {
		t.Fatalf("list = %q, %v", out, err)
	}
}

func TestEvidenceListWithoutRuntime(t *testing.T) {
	h := &CommandHandler{ws: NewWorkspace(nil, "proj", "sess")}
	out, err := h.handleEvidenceList(context.Background())
	if err != nil || !strings.Contains(out, "unavailable (no runtime attached)") {
		t.Fatalf("list = %q, %v", out, err)
	}
}
