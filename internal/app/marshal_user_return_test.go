package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// handedInFixture drives one task to the point where it awaits a decision.
func handedInFixture(t *testing.T) *MarshalService {
	t.Helper()
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
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
	return s
}

// onlyApproves answers the service's approval question for one purpose, as
// the TUI does after the person typed the matching command.
func onlyApproves(purpose string) func(context.Context, string, string) (string, error) {
	return func(_ context.Context, _, asked string) (string, error) {
		if asked != purpose {
			return "", errors.New("no approval")
		}
		return "operator:test", nil
	}
}

func TestMarshalUserReturnRecordsReasonAndReworks(t *testing.T) {
	ctx := context.Background()
	s := handedInFixture(t)
	s.ApprovalActor = onlyApproves("return:a")
	if _, err := s.ReturnByUser(ctx, "run", "a", "  "); err == nil {
		t.Fatal("return without a reason accepted")
	}
	v, err := s.ReturnByUser(ctx, "run", "a", "the heading is wrong")
	if err != nil || v != marshal.VerdictReturn {
		t.Fatalf("return: %s %v", v, err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil || run.Tasks[0].State != marshal.Returned || run.Tasks[0].ReturnsByAgent["worker"] != 1 {
		t.Fatalf("task after return: %+v %v", run.Tasks[0], err)
	}
	review, err := s.Store.GetMarshalReview(ctx, "run", "a", 1)
	if err != nil || review.Value.Verdict != marshal.VerdictReturn || review.Value.Reviewer != "operator:test" || len(review.Value.Reasons) != 1 || review.Value.Reasons[0] != "the heading is wrong" {
		t.Fatalf("stored review: %+v %v", review.Value, err)
	}
	if _, err := s.Dispatch(ctx, "run", "a", "rework"); err != nil {
		t.Fatalf("returned task could not be reworked: %v", err)
	}
}

// A person's returns count against the rework limit exactly like a
// reviewer's: the second return moves the task to another worker.
func TestMarshalUserReturnFollowsReworkLimit(t *testing.T) {
	ctx := context.Background()
	s := handedInFixture(t)
	s.ApprovalActor = onlyApproves("return:a")
	if _, err := s.ReturnByUser(ctx, "run", "a", "first"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "rework")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	v, err := s.ReturnByUser(ctx, "run", "a", "second")
	if err != nil || v != marshal.VerdictReassign {
		t.Fatalf("second return: %s %v", v, err)
	}
	if review, err := s.Store.GetMarshalReview(ctx, "run", "a", 2); err != nil || review.Value.Reasons[0] != "second" {
		t.Fatalf("second attempt review: %+v %v", review.Value, err)
	}
}

func TestMarshalUserReturnNeedsThePersonAndAHandIn(t *testing.T) {
	ctx := context.Background()
	s := handedInFixture(t)
	s.ApprovalActor = onlyApproves("accept")
	if _, err := s.ReturnByUser(ctx, "run", "a", "reason"); err == nil || !strings.Contains(err.Error(), "not given by the person") {
		t.Fatalf("return without the person's approval: %v", err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil || run.Tasks[0].State != marshal.HandedIn {
		t.Fatalf("refused return changed the task: %+v %v", run.Tasks[0], err)
	}
	if _, err := s.Store.GetMarshalReview(ctx, "run", "a", 1); err == nil {
		t.Fatal("refused return stored a review")
	}
	s2, _ := marshalFixture(t, 1)
	if _, err := s2.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	s2.ApprovalActor = onlyApproves("return:a")
	if _, err := s2.ReturnByUser(ctx, "run", "a", "reason"); err == nil || !strings.Contains(err.Error(), "not awaiting a decision") {
		t.Fatalf("return of a task with no hand-in: %v", err)
	}
}
