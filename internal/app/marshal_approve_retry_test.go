package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/marshal"
	"testing"
)

func TestMarshalApproveRetryDoesNotReapproveAndDispatchesQueuedGovernedTask(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	ctx := t.Context()
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	actor := s.ApprovalActor
	s.ApprovalActor = func(ctx context.Context, runID, purpose string) (string, error) {
		calls++
		return actor(ctx, runID, purpose)
	}
	first, err := s.Approve(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Approve(ctx, "run")
	if err != nil || retry.PlanVersion != first.PlanVersion || calls != 1 {
		t.Fatalf("retry=%+v calls=%d err=%v", retry, calls, err)
	}
	run, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err != nil || run.Tasks[0].State != marshal.Merged {
		t.Fatalf("governed dispatch=%+v err=%v", run, err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatalf("approval after dispatch: %v", err)
	}
	events, err := s.Store.MarshalDecisions(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == "marshal.plan.approved" {
			count++
		}
	}
	if count != 1 || calls != 1 {
		t.Fatalf("approval events=%d actor calls=%d", count, calls)
	}
}
