//go:build !governed

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// The Marshal accepts a hand-in and the required independent review returns
// it. The refusal must name the independent review as the cause, ahead of the
// reviewer's own reasons, not leave the Marshal's "accepted" reasons followed
// only by a bare constitution code.
func TestCrossReviewReturnNamesTheReviewAsTheRefusal(t *testing.T) {
	ctx := context.Background()
	extremeProviderCLIs(t)
	s, _ := marshalFixture(t, 1)
	s.Gate = cloud.Authorize(ctx, cloud.Config{}, t.TempDir(), "test").Gate
	s.ModelProvider = "model"
	s.ApprovalActor = func(context.Context, string, string) (string, error) { return "marshal-extreme", nil }
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
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "second", Reasons: []string{"a password value leaks to the cloud"}, EvidenceRefs: []string{"check:test -f a.txt"}}, "other", nil
	}
	v, err := s.Review(ctx, "run", "a", knownCharge())
	if err != nil || v == marshal.VerdictAccept {
		t.Fatalf("review %s %v", v, err)
	}
	review, err := s.Store.GetMarshalReview(ctx, "run", "a", 1)
	if err != nil {
		t.Fatal(err)
	}
	reasons := review.Value.Reasons
	last := reasons[len(reasons)-1]
	if !strings.Contains(last, "acceptance refused: the independent review by other did not accept it (return)") || !strings.Contains(last, "CONST_COMPLETION_NOT_AUTHORIZED") {
		t.Fatalf("refusal does not name the independent review: %q", reasons)
	}
	if review.Value.Independent == nil || review.Value.Independent.Reasons[0] != "a password value leaks to the cloud" {
		t.Fatalf("independent reasons lost: %+v", review.Value.Independent)
	}
}

// A task returned again after its reassignment stops for the person, and the
// stop's Enter gives it its working way on (retry, then resume) instead of a
// plain resume that leaves it escalated. Earlier returns stay counted.
func TestReworkLimitEscalationStopRetriesTheTask(t *testing.T) {
	run := marshal.Run{Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "router", Worker: "agy", State: marshal.HandedIn, ReturnsByAgent: map[string]int{"codex": 2, "agy": 0}}}}
	if got := applyMarshalReturn(&run, &run.Tasks[0]); got != marshal.VerdictEscalate {
		t.Fatalf("verdict %s, want escalate", got)
	}
	p := run.Pause
	if p == nil || p.Code != marshal.CodeReworkLimit || p.Action != marshal.PauseRetryTask || p.TaskID != "router" {
		t.Fatalf("stop has no working path: %+v", p)
	}
	if next := strings.Join(p.Resolutions, " "); !strings.Contains(next, "/marshal retry router") || !strings.Contains(next, "/marshal reassign router") {
		t.Fatalf("stop does not say how to go on: %q", next)
	}
	if run.Tasks[0].ReturnsByAgent["codex"] != 2 || run.Tasks[0].ReturnsByAgent["agy"] != 1 {
		t.Fatalf("returns not kept: %+v", run.Tasks[0].ReturnsByAgent)
	}
}
