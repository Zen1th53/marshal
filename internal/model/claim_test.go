package model_test

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestParseClaimState(t *testing.T) {
	validStates := []model.ClaimState{
		model.ClaimStateUnsupported,
		model.ClaimStateSupported,
		model.ClaimStateVerified,
		model.ClaimStateContested,
		model.ClaimStateStale,
		model.ClaimStateInvalidated,
	}

	for _, s := range validStates {
		parsed, err := model.ParseClaimState(string(s))
		if err != nil {
			t.Fatalf("ParseClaimState(%q) unexpected error: %v", s, err)
		}
		if parsed != s {
			t.Fatalf("ParseClaimState(%q) = %q, want %q", s, parsed, s)
		}
		if !parsed.IsValid() {
			t.Fatalf("expected valid for %q", parsed)
		}
	}

	// Non-negotiable: PROVEN and PARTIALLY_VERIFIED must be explicitly rejected
	if _, err := model.ParseClaimState("PROVEN"); err != model.ErrProvenBanned {
		t.Fatalf("ParseClaimState(PROVEN) err = %v, want ErrProvenBanned", err)
	}
	if _, err := model.ParseClaimState("PARTIALLY_VERIFIED"); err != model.ErrPartiallyVerifiedBanned {
		t.Fatalf("ParseClaimState(PARTIALLY_VERIFIED) err = %v, want ErrPartiallyVerifiedBanned", err)
	}
	if _, err := model.ParseClaimState("RANDOM_STATE"); err == nil {
		t.Fatalf("ParseClaimState(RANDOM_STATE) expected error, got nil")
	}
}

func TestClaimCriticality(t *testing.T) {
	crits := []struct {
		crit       model.ClaimCriticality
		isCritical bool
	}{
		{model.CriticalityBlocker, true},
		{model.CriticalityFeature, true},
		{model.CriticalityStandard, false},
		{model.CriticalityInformational, false},
	}

	for _, c := range crits {
		if !c.crit.IsValid() {
			t.Errorf("criticality %q should be valid", c.crit)
		}
		if c.crit.IsCritical() != c.isCritical {
			t.Errorf("criticality %q IsCritical = %v, want %v", c.crit, c.crit.IsCritical(), c.isCritical)
		}
	}
}

func TestClaimValidate(t *testing.T) {
	valid := model.Claim{
		ID:             "CLAIM-001",
		GoalID:         "GOAL-100",
		GoalRevision:   1,
		Subject:        "auth.token.verification",
		NormalizedText: "Token signature verified using Ed25519",
		Scope:          "auth",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateUnsupported,
		Author: model.AuthorProvenance{
			AgentID: "codex-1",
			Harness: "codex-cli",
		},
		CreatedAt: time.Now().UTC(),
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid claim, got: %v", err)
	}

	invalid := valid
	invalid.ID = ""
	if err := invalid.Validate(); err == nil {
		t.Errorf("expected error on empty ID")
	}

	invalid = valid
	invalid.Criticality = "UNKNOWN"
	if err := invalid.Validate(); err == nil {
		t.Errorf("expected error on invalid criticality")
	}

	invalid = valid
	invalid.State = "PROVEN"
	if err := invalid.Validate(); err == nil {
		t.Errorf("expected error on banned state PROVEN")
	}
}
