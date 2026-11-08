package app

import (
	"strings"
	"testing"
)

func TestClassifyInfraFailureNamesKind(t *testing.T) {
	for text, want := range map[string]string{
		"Claude AI usage limit reached|1793900000":                                 "provider quota or rate limit",
		"You've hit your limit · resets 3pm (Asia/Tashkent)":                       "provider quota or rate limit",
		"credential broker unavailable; Antigravity sign-in: access token expired": "provider sign-in or credentials",
		"process 05: failed task was not limited by its provider":                  "provider cooldown or failover refusal",
		"dial tcp 1.2.3.4:443: connection refused":                                 "transport or network",
		"codex: error: Exceeded skills context budget":                             "harness context or tool output",
		"run failed: one or more tasks failed":                                     "worker process failure",
	} {
		if got, reason := classifyInfraFailure(text); got != want || reason == "" {
			t.Errorf("%q: got %q (%q), want %q", text, got, reason, want)
		}
	}
	if _, reason := classifyInfraFailure(strings.Repeat("x ", 400)); len(reason) > 300 {
		t.Fatalf("reason not bounded: %d", len(reason))
	}
}

// An exhausted infrastructure retry is never permanent, even when the
// provider's own message mentions a budget; other budget escalations are.
func TestInfraRetryEscalationIsRetryable(t *testing.T) {
	if isPermanentEscalationReason(infraRetryEscalation + ": 7 attempts ended without a hand-in; last on codex: Exceeded skills context budget") {
		t.Fatal("infra retry escalation is permanent")
	}
	if !isPermanentEscalationReason("task budget exceeded") {
		t.Fatal("budget escalation became retryable")
	}
}
