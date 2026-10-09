package tui

import (
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"strings"
	"testing"
)

func TestStatusShowsVerifierFindingsAndMixedEvidence(t *testing.T) {
	panel := &MarshalPanel{RunID: "run", Report: &app.MarshalCompletionReport{
		Criteria:  []app.MarshalCriterionReport{{TaskID: "a", Criterion: "c", Status: "failed", Mixed: true, Incomplete: true}},
		Verifiers: []marshal.VerifierEvidence{{Reviewer: "verifier:test", Provider: "test", SessionID: "fresh-session", Commit: "commit", Verdict: "pass", InputDigest: "digest", Findings: []string{"boundary reviewed"}}},
	}}
	text := marshalStatusText(panel)
	for _, value := range []string{"mixed pass/fail", "incomplete", "boundary reviewed", "verifier:test", "fresh-session", "commit", "digest"} {
		if !strings.Contains(text, value) {
			t.Fatalf("status missing %q: %s", value, text)
		}
	}
}
