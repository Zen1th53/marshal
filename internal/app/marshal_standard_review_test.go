package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestStandardHandInRequiresIndependentCrossReview(t *testing.T) {
	ctx := context.Background()
	for _, scenario := range []string{"unavailable", "worker-session", "refused", "fresh-session-fallback"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			s.Gate = nil
			if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
				t.Fatal(err)
			}
			_, err := s.Approve(ctx, "run")
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.Dispatch(ctx, "run", "a", "write")
			if err != nil {
				t.Fatal(err)
			}
			if tier, err := s.dispatchedTier(ctx, "run", "a"); err != nil || tier != marshal.Standard {
				t.Fatalf("tier = %s: %v", tier, err)
			}
			h, err := s.CollectHandIn(ctx, "run", d)
			if err != nil {
				t.Fatal(err)
			}
			s.CrossReview = nil
			if scenario != "unavailable" {
				s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
					verdict := marshal.VerdictAccept
					provider := h.Provider
					if scenario == "refused" {
						verdict, provider = marshal.VerdictReturn, "other"
					}
					review := marshal.Review{Verdict: verdict, Reviewer: "independent", EvidenceRefs: []string{"result:" + h.ResultCommit}}
					if scenario == "fresh-session-fallback" {
						review.FreshSession, review.SessionID = true, "fresh-independent"
					}
					return review, provider, nil
				}
			}
			v, err := s.Review(ctx, "run", "a", knownCharge())
			if scenario == "fresh-session-fallback" {
				if err != nil || v != marshal.VerdictAccept {
					t.Fatalf("fresh same-provider review refused: %s %v", v, err)
				}
				return
			}
			if err == nil && v == marshal.VerdictAccept {
				t.Fatal("Standard hand-in accepted without an independent acceptance")
			}
			stored, _, loadErr := s.load(ctx, "run")
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if stored.Tasks[0].State == marshal.Accepted || stored.Tasks[0].State == marshal.Merged {
				t.Fatalf("task accepted: %+v", stored.Tasks[0])
			}
			if scenario == "unavailable" && (stored.Pause == nil || !strings.Contains(stored.Pause.Reason, "cross-review")) {
				t.Fatalf("missing review pause: %+v", stored.Pause)
			}
		})
	}
}

func TestStandardRunCannotCloseWithoutIndependentFinalCheck(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	s.CrossReview = func(_ context.Context, _ marshal.Task, h marshal.HandIn, _ marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "independent", EvidenceRefs: []string{"result:" + h.ResultCommit}}, "other", nil
	}
	if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review: %s %v", v, err)
	}
	if err := s.Merge(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}
	s.ModelProvider = "model"
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "other", nil }
	s.IndependentVerify = nil
	if err := s.Close(ctx, "run"); err == nil {
		t.Fatal("Standard run closed without a final checker")
	}
	run, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.State == marshal.Closed || run.Pause == nil || !strings.Contains(run.Pause.Detail, "independent verifier") {
		t.Fatalf("missing verification pause: %+v", run)
	}
	// Even a selected verifier must supply a passing, commit-bound final check.
	if _, err := s.Resume(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	s.IndependentVerify = func(context.Context, marshal.Run, string, verification.Session) (marshal.VerifierEvidence, error) {
		return marshal.VerifierEvidence{Verdict: "fail"}, nil
	}
	if err := s.Close(ctx, "run"); err == nil {
		t.Fatal("Standard run closed after a refused final check")
	}
	run, _, err = s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.State == marshal.Closed {
		t.Fatal("run closed")
	}
}
