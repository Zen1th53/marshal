package authority_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/authority"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT153FactVsPreferenceVsPolicySeparation(t *testing.T) {
	ctx := context.Background()
	resolver := authority.NewTierResolver()

	policyRec := model.MemoryRecordV2{
		ID:        "POL-HTTPS-ENFORCE",
		Title:     "Enforce TLS/HTTPS",
		Body:      "All outbound telemetry must use TLS 1.3",
		Authority: model.AuthorityPolicy,
		Lifecycle: model.MemoryDurable,
	}

	preferenceRec := model.MemoryRecordV2{
		ID:        "PREF-USER-HTTP",
		Title:     "User Preference",
		Body:      "Use plain HTTP for local telemetry endpoints",
		Authority: model.AuthorityAgent,
		Kind:      model.MemoryKindFinding,
	}

	factRec := model.MemoryRecordV2{
		ID:        "FACT-TOOL-UNAVAILABLE",
		Title:     "Tool Availability Fact",
		Body:      "Tool sqlite-vss is not installed in current environment",
		Authority: model.AuthorityVerified,
		Lifecycle: model.MemoryDurable,
	}

	// 1. Conflict: Preference vs Policy -> Policy wins unconditionally
	resolved, err := resolver.ResolvePrecedence(ctx, policyRec, preferenceRec)
	if err != nil {
		t.Fatalf("ResolvePrecedence Policy vs Pref: %v", err)
	}
	if resolved.WinningRecord.ID != policyRec.ID || resolved.SuppressedRecord.ID != preferenceRec.ID {
		t.Fatalf("expected policy to win over preference, got winner: %s", resolved.WinningRecord.ID)
	}

	// 2. Conflict: Fact vs Preference -> Fact wins
	resolvedFact, err := resolver.ResolvePrecedence(ctx, factRec, preferenceRec)
	if err != nil {
		t.Fatalf("ResolvePrecedence Fact vs Pref: %v", err)
	}
	if resolvedFact.WinningRecord.ID != factRec.ID {
		t.Fatalf("expected verified fact to win over preference, got winner: %s", resolvedFact.WinningRecord.ID)
	}

	// 3. Valid personalization (non-conflicting preference) is retained
	validPref := model.MemoryRecordV2{
		ID:        "PREF-THEME-DARK",
		Title:     "Editor Theme",
		Body:      "Prefer dark mode syntax highlighting",
		Authority: model.AuthorityAgent,
	}
	accepted := resolver.CanApplyPersonalization(validPref, []model.MemoryRecordV2{policyRec, factRec})
	if !accepted {
		t.Fatal("expected valid non-conflicting preference to be accepted")
	}
}
