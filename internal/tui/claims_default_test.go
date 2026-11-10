//go:build governed

package tui

import "testing"

const archGoldenName, helpGoldenName = "arch-governed", "help-governed"

var demoArchRules = append([]string{"Marshal: local ULTRA, anonymous opt-out telemetry"}, archRules...)

func TestGovernedClaimsUnchanged(t *testing.T) {
	if len(archComponents) != 9 {
		t.Errorf("len(archComponents) = %d, want 9", len(archComponents))
	}
	if bpD1 != "plan approved? (/marshal approve)" {
		t.Errorf("bpD1 = %q, want %q", bpD1, "plan approved? (/marshal approve)")
	}
	if got := len(archRulesInForce(true)); got != len(archRules) {
		t.Errorf("len(archRulesInForce(true)) = %d, want len(archRules) = %d", got, len(archRules))
	}
}
