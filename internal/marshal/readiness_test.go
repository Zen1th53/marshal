package marshal

import (
	"strings"
	"testing"
	"time"
)

func TestReadinessPlannerLeads(t *testing.T) {
	providers := map[string]ProviderReadiness{"codex": {Installed: true}, "claude": {Installed: true, SignedIn: true}}
	r := ComputeReadiness("codex", providers, ReviewPolicy{})
	if r.PlannerRunnable || !strings.Contains(r.Blocker, "codex login") || !r.FinalCheckEligible || len(r.Workers) != 1 {
		t.Fatalf("%+v", r)
	}
	providers["codex"] = ProviderReadiness{Installed: true, SignedIn: true}
	if r := ComputeReadiness("codex", providers, ReviewPolicy{}); r.Blocker != "" || !r.PlannerRunnable {
		t.Fatalf("%+v", r)
	}
}

func TestReadinessFinalGuarantees(t *testing.T) {
	p := map[string]ProviderReadiness{"codex": {Installed: true, SignedIn: true}}
	for _, tc := range []struct {
		name              string
		policy            ReviewPolicy
		eligible, confirm bool
		text              string
	}{
		{"codex only", ReviewPolicy{}, true, false, "native model identity"},
		{"pinned unavailable", ReviewPolicy{FinalProvider: "claude"}, false, true, "closure will wait"},
		{"checks only", ReviewPolicy{FinalModel: FinalModelOff}, true, true, "Checks-only delivery"},
		{"review off", ReviewPolicy{CrossReview: CrossReviewOff}, true, true, "Review is Off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := ComputeReadiness("codex", p, tc.policy)
			if r.FinalCheckEligible != tc.eligible || r.NeedsConfirmation != tc.confirm || !strings.Contains(r.Guarantee, tc.text) {
				t.Fatalf("%+v", r)
			}
			if tc.policy.FinalModel == FinalModelOff && r.Blocker != "" {
				t.Fatal(r.Blocker)
			}
		})
	}
}

func TestReadinessMissingAndLimitedPlanner(t *testing.T) {
	for _, p := range []ProviderReadiness{{}, {Installed: true, AuthUnknown: true}, {Installed: true, SignedIn: true, Unavailable: "quota exhausted"}} {
		r := ComputeReadiness("codex", map[string]ProviderReadiness{"codex": p}, ReviewPolicy{})
		if r.PlannerRunnable || r.Blocker == "" {
			t.Fatalf("%+v", r)
		}
	}
}

func TestFinalProviderPolicySnapshot(t *testing.T) {
	policy := DefaultReviewPolicy()
	policy.FinalProvider = "codex"
	snapshot := NewReviewPolicySnapshot(policy, "digest", "owner", time.Now())
	policy.FinalProvider = "claude"
	if snapshot.Policy().FinalProvider != "codex" || snapshot.Validate() != nil {
		t.Fatal("final provider pin not preserved")
	}
	snapshot.FinalProvider = ""
	if snapshot.Policy().FinalProvider != "any" {
		t.Fatal("legacy snapshot did not default to any provider")
	}
	snapshot.FinalProvider = "unknown"
	if snapshot.Validate() == nil {
		t.Fatal("unknown provider accepted")
	}
}
