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

func TestStatusShowsCompletionReportLabels(t *testing.T) {
	panel := &MarshalPanel{RunID: "run", Report: &app.MarshalCompletionReport{
		ReviewLabel: "Standard: review by the Marshal itself; independent review in ULTRA",
		ChecksLabel: "approved checks passed",
	}}
	text := marshalStatusText(panel)
	if !strings.Contains(text, "Standard: review by the Marshal itself; independent review in ULTRA") {
		t.Fatalf("status missing review label: %s", text)
	}
	if !strings.Contains(text, "approved checks passed") {
		t.Fatalf("status missing checks label: %s", text)
	}
}

func TestStatusHidesCompletionReportBeforeTasksHaveResults(t *testing.T) {
	draftingPanel := &MarshalPanel{
		RunID: "run-draft",
		Tasks: []MarshalTaskRow{{
			ID:     "bye",
			Worker: "codex",
			State:  marshal.Queued,
		}},
		Report: &app.MarshalCompletionReport{
			ReviewLabel: "Standard: review by the Marshal itself; independent review in ULTRA",
			ChecksLabel: "not tested",
			Criteria:    []app.MarshalCriterionReport{{TaskID: "bye", Criterion: "check bytes", Status: "not tested"}},
		},
	}
	text := marshalStatusText(draftingPanel)
	if strings.Contains(text, "completion report:") {
		t.Fatalf("drafting panel should not show completion report, got:\n%s", text)
	}

	handedInPanel := &MarshalPanel{
		RunID: "run-draft",
		Tasks: []MarshalTaskRow{{
			ID:     "bye",
			Worker: "codex",
			State:  marshal.HandedIn,
		}},
		Report: &app.MarshalCompletionReport{
			ReviewLabel: "Standard: review by the Marshal itself; independent review in ULTRA",
			ChecksLabel: "approved checks passed",
			Criteria:    []app.MarshalCriterionReport{{TaskID: "bye", Criterion: "check bytes", Status: "verified"}},
		},
	}
	text2 := marshalStatusText(handedInPanel)
	if !strings.Contains(text2, "completion report:") {
		t.Fatalf("handed-in panel must show completion report, got:\n%s", text2)
	}
}
