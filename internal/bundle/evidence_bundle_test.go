package bundle

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestEvidenceBundleCreationAndIntegrity(t *testing.T) {
	goal := model.GoalContract{
		ID:             "goal-test",
		Revision:       1,
		DesiredOutcome: "Deliver verified system",
	}

	claims := []model.Claim{
		{
			ID:          "cl-1",
			Criticality: model.CriticalityBlocker,
			State:       model.ClaimStateVerified,
		},
		{
			ID:          "cl-2",
			Criticality: model.CriticalityInformational,
			State:       model.ClaimStateSupported,
		},
	}

	evidence := []model.EvidenceRef{
		{
			EvidenceID:      "ev-1",
			Tool:            "go test",
			Digest:          "sha256:123456",
			IsDeterministic: true,
		},
	}

	participants := []model.Participant{
		{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex"},
	}

	b, err := NewEvidenceBundle(
		"bundle-1",
		goal,
		"sha256:constraintsdigest",
		"6fd6189",
		participants,
		claims,
		evidence,
		[]string{"item-1"},
	)
	if err != nil {
		t.Fatalf("create evidence bundle error: %v", err)
	}

	if err := b.Validate(); err != nil {
		t.Fatalf("bundle validation error: %v", err)
	}

	if b.BundleType != "Evidence Bundle" {
		t.Fatalf("expected BundleType 'Evidence Bundle', got %q", b.BundleType)
	}
	if len(b.CriticalClaims) != 1 || b.CriticalClaims[0].ID != "cl-1" {
		t.Fatalf("expected only critical claims filtered into bundle, got %d", len(b.CriticalClaims))
	}
	if b.BundleDigest == "" {
		t.Fatalf("bundle digest cannot be empty")
	}

	// Invariant: "Proof Bundle" naming is strictly prohibited
	b.BundleType = "Proof Bundle"
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "naming an artifact 'Proof Bundle' is prohibited") {
		t.Fatalf("expected Proof Bundle error, got: %v", err)
	}
}
