package app

import (
	"encoding/json"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestAcceptedCrossReviewsBoundToCurrentTaskAttempt(t *testing.T) {
	for _, scenario := range []string{"accepted", "old-attempt", "wrong-commit", "same-provider", "returned", "unresolved-evidence"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			startIntegrityRun(t, s, marshal.Budget{})
			run := marshal.Run{Tasks: []marshal.Task{{PlanTaskID: "a", Worker: "claude", State: marshal.Merged, ResultCommit: "head", EvidenceAttemptBase: 1}}}
			h := marshal.HandIn{Provider: "claude", Worker: "claude", ResultCommit: "head", Diff: "changes"}
			independent := &marshal.IndependentReview{Provider: "codex", Reviewer: "cross-review:codex", Verdict: marshal.VerdictAccept, EvidenceRefs: []string{"diff"}}
			switch scenario {
			case "old-attempt":
				run.Tasks[0].EvidenceAttemptBase = 2
			case "wrong-commit":
				run.Tasks[0].ResultCommit = "new-head"
			case "same-provider":
				independent.Provider = "claude-code"
			case "returned":
				independent.Verdict = marshal.VerdictReturn
			case "unresolved-evidence":
				independent.EvidenceRefs = []string{"result:invented"}
			}
			review := marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal:claude", Independent: independent}
			if _, err := s.Store.SetMarshalHandIn(t.Context(), "run", "a", 2, h); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Store.SetMarshalReview(t.Context(), "run", "a", 2, review); err != nil {
				t.Fatal(err)
			}
			payloads, err := s.acceptedCrossReviewPayloads(t.Context(), "run", run)
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "accepted" {
				if len(payloads) != 0 {
					t.Fatalf("invalid review promoted: %v", payloads)
				}
				return
			}
			if len(payloads) != 1 {
				t.Fatalf("accepted review absent: %v", payloads)
			}
			for _, raw := range payloads {
				var record map[string]any
				if err := json.Unmarshal(raw, &record); err != nil {
					t.Fatal(err)
				}
				for key, want := range map[string]any{"task": "a", "attempt": float64(2), "result_commit": "head", "worker_provider": "claude", "reviewer_provider": "codex", "verdict": "accept"} {
					if record[key] != want {
						t.Errorf("%s = %v, want %v (%s)", key, record[key], want, raw)
					}
				}
			}
		})
	}
}
