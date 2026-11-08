//go:build !governed

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestExtremeProcess05ExactApprovalWaitSurvivesResumeWithoutRework(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "wait-run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "wait-run"); err != nil {
		t.Fatal(err)
	}
	run, revision, err := s.load(t.Context(), "wait-run")
	if err != nil {
		t.Fatal(err)
	}
	run.State, run.Tasks[0].State = marshal.Dispatching, marshal.Dispatched
	if err := s.save(t.Context(), "wait-run", run, revision); err != nil {
		t.Fatal(err)
	}
	pending, invalid := true, false
	s.process05WaitReceipt = func(_ context.Context, _ marshal.Run, task marshal.Task, runID, approvalID string) (bool, error) {
		if task.PlanTaskID != "a" || runID != "owned-lane" || approvalID != "exact-approval" {
			t.Fatal("unbound continuation")
		}
		if invalid {
			return false, execution.ErrRunConflict
		}
		return pending, nil
	}
	if handled, err := s.pausePrivateProcess05Approval(t.Context(), "wait-run", MarshalDispatch{TaskID: "a"}, process05ApprovalRequired("owned-lane", "a", "exact-approval")); !handled || err != nil {
		t.Fatalf("pause: %v %v", handled, err)
	}
	waiting, err := s.Resume(t.Context(), "wait-run")
	if err != nil || waiting.State != marshal.AwaitingUser || waiting.Tasks[0].State != marshal.Queued || len(waiting.Tasks[0].ReturnsByAgent) != 0 {
		t.Fatalf("pending approval became restart rework: %+v %v", waiting, err)
	}
	invalid = true
	if _, err := s.Resume(t.Context(), "wait-run"); !errors.Is(err, execution.ErrRunConflict) {
		t.Fatalf("invalid receipt resumed: %v", err)
	}
	invalid, pending = false, false
	resumed, err := s.Resume(t.Context(), "wait-run")
	if err != nil || resumed.State != marshal.Dispatching || resumed.Tasks[0].State != marshal.Queued || len(resumed.Tasks[0].ReturnsByAgent) != 0 {
		t.Fatalf("exact approval failed continuation: %+v %v", resumed, err)
	}
}

func TestExtremeProcess05ApprovalWaitRejectsUnconfirmedReceipt(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "wait-run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "wait-run"); err != nil {
		t.Fatal(err)
	}
	run, revision, err := s.load(t.Context(), "wait-run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].State = marshal.Dispatched
	if err := s.save(t.Context(), "wait-run", run, revision); err != nil {
		t.Fatal(err)
	}
	// A receipt that cannot confirm the exact binding refuses the wait and
	// leaves the task untouched.
	s.process05WaitReceipt = func(context.Context, marshal.Run, marshal.Task, string, string) (bool, error) {
		return false, execution.ErrRunConflict
	}
	if handled, err := s.pausePrivateProcess05Approval(t.Context(), "wait-run", MarshalDispatch{TaskID: "a"}, process05ApprovalRequired("owned-lane", "a", "exact-approval")); !handled || !errors.Is(err, execution.ErrRunConflict) {
		t.Fatalf("unconfirmed wait admitted: %v %v", handled, err)
	}
	current, _, err := s.load(t.Context(), "wait-run")
	if err != nil || current.Tasks[0].State != marshal.Dispatched || current.Tasks[0].Process05WaitApprovalID != "" {
		t.Fatal("refused receipt changed task")
	}
	// A confirmed binding whose approval was already decided (the Marshal
	// answered before the wait was recorded) parks the task without pausing.
	s.process05WaitReceipt = func(context.Context, marshal.Run, marshal.Task, string, string) (bool, error) { return false, nil }
	if handled, err := s.pausePrivateProcess05Approval(t.Context(), "wait-run", MarshalDispatch{TaskID: "a"}, process05ApprovalRequired("owned-lane", "a", "exact-approval")); !handled || err != nil {
		t.Fatalf("decided wait refused: %v %v", handled, err)
	}
	current, _, err = s.load(t.Context(), "wait-run")
	if err != nil || current.Tasks[0].State != marshal.Queued || current.Tasks[0].Process05WaitApprovalID != "exact-approval" || current.Pause != nil {
		t.Fatalf("decided wait not parked: %+v %+v", current.Tasks[0], current.Pause)
	}
}
