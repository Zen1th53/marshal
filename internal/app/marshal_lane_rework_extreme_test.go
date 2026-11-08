//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// A reassignment or retry gives a worker a fresh return count, but the lane
// numbering keeps every retired return: the next attempt never reuses an
// earlier attempt's lane.
func TestRetiredReturnsKeepLaneNumbering(t *testing.T) {
	task := marshal.Task{ReturnsByAgent: map[string]int{"codex": 4, "claude": 1}}
	before := taskLaneRework(task)
	retireWorkerReturns(&task, "codex")
	if task.ReturnsByAgent["codex"] != 0 || taskLaneRework(task) != before {
		t.Fatalf("returns=%v lane rework=%d, want %d", task.ReturnsByAgent, taskLaneRework(task), before)
	}
	task.ReturnsByAgent["codex"]++
	if taskLaneRework(task) != before+1 {
		t.Fatalf("lane rework after a new return = %d, want %d", taskLaneRework(task), before+1)
	}
}

// A task reassigned back to a worker that was returned before hands in under
// a new attempt number: the retired returns still count, so the hand-in
// never collides with an earlier attempt's record.
func TestReassignedBackWorkerHandsInUnderNewAttempt(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	write := func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte(req.Task.Worker+"\n"), 0600)
	}
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: write}
	s.Drivers["other"] = driver.Governed{Provider: "other", Run: write}
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	draft := s.Model.(marshalFakeModel).draft
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "marshal"}}
	round := func() {
		t.Helper()
		d, err := s.Dispatch(ctx, "run", "a", "write")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
			t.Fatalf("hand-in: %v", err)
		}
		if _, err = s.Review(ctx, "run", "a", knownCharge()); err != nil {
			t.Fatal(err)
		}
	}
	round()
	round()
	if err := s.Reassign(ctx, "run", "a", "other"); err != nil {
		t.Fatal(err)
	}
	round()
	run, _, err := s.load(ctx, "run")
	if err != nil || run.Tasks[0].State != marshal.Escalated {
		t.Fatalf("second worker's return did not escalate: %s %v", run.Tasks[0].State, err)
	}
	if err := s.Reassign(ctx, "run", "a", "worker"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "rework")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatalf("reassigned-back hand-in collided with an earlier attempt: %v", err)
	}
	run, _, err = s.load(ctx, "run")
	if err != nil || 1+run.Tasks[0].EvidenceAttemptBase+taskReturns(run.Tasks[0]) != 4 {
		t.Fatalf("attempt = %d, want 4 (%v)", 1+run.Tasks[0].EvidenceAttemptBase+taskReturns(run.Tasks[0]), err)
	}
}

// A hand-in retained by an earlier build with an attempt number another
// attempt already holds completes on resume under its own next attempt,
// instead of failing on the hand-in record's unique key forever.
func TestRetainedHandInWithHeldAttemptCompletesUnderItsOwnAttempt(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	write := func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte(req.Task.Worker+" "+req.Brief+"\n"), 0600)
	}
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: write}
	s.Drivers["other"] = driver.Governed{Provider: "other", Run: write}
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	draft := s.Model.(marshalFakeModel).draft
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "marshal"}}
	for round := 0; round < 3; round++ {
		if round == 2 {
			if err := s.Reassign(ctx, "run", "a", "other"); err != nil {
				t.Fatal(err)
			}
		}
		d, err := s.Dispatch(ctx, "run", "a", fmt.Sprintf("write %d", round))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Review(ctx, "run", "a", knownCharge()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Reassign(ctx, "run", "a", "worker"); err != nil {
		t.Fatal(err)
	}
	// The hand-in's effect is interrupted, so its operation is retained.
	s.AfterLifecycleEffect = func(kind, _ string) error {
		if kind == "hand-in" {
			return errors.New("interrupted")
		}
		return nil
	}
	d, err := s.Dispatch(ctx, "run", "a", "rework")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err == nil {
		t.Fatal("interrupted hand-in completed")
	}
	s.AfterLifecycleEffect = nil
	record, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run")
	if err != nil || record.Value.Operation == nil || record.Value.Operation.HandIn == nil || record.Value.Operation.Attempt != 4 {
		t.Fatalf("hand-in operation was not retained at attempt 4: %+v %v", record.Value.Operation, err)
	}
	// What an earlier build retained: the same hand-in under a held attempt.
	stale := record.Value
	stale.Operation.Attempt = 2
	if err = s.Store.SaveMarshalState(ctx, s.ProjectID, "run", stale, record.Revision, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.recoverOperation(ctx, "run", run, rev); err != nil {
		t.Fatalf("retained hand-in did not complete: %v", err)
	}
	stored, err := s.Store.GetMarshalHandIn(ctx, "run", "a", 4)
	if err != nil || stored.Value.ResultCommit != record.Value.Operation.HandIn.ResultCommit {
		t.Fatalf("hand-in not recorded under its own attempt: %v", err)
	}
	earlier, err := s.Store.GetMarshalHandIn(ctx, "run", "a", 2)
	if err != nil || earlier.Value.ResultCommit == stored.Value.ResultCommit {
		t.Fatalf("earlier attempt's hand-in was overwritten: %v", err)
	}
	run, _, err = s.load(ctx, "run")
	if err != nil || run.Operation != nil || run.Tasks[0].State != marshal.HandedIn {
		t.Fatalf("task did not reach its hand-in: %s %v", run.Tasks[0].State, err)
	}
}

// Only the operator's latest reassignment of the task to exactly the worker
// authorizes a provider switch at dispatch.
func TestOperatorReassignmentAuthorizesOnlyItsLatestWorker(t *testing.T) {
	op := events.Event{ID: "e1", TaskID: "a", Type: events.EventTypeMarshalTaskReassigned, Data: map[string]any{"worker": "claude", "actor": "operator"}}
	runtime := events.Event{ID: "e2", TaskID: "a", Type: events.EventTypeMarshalTaskReassigned, Data: map[string]any{"worker": "claude"}}
	other := events.Event{ID: "e3", TaskID: "b", Type: events.EventTypeMarshalTaskReassigned, Data: map[string]any{"worker": "claude", "actor": "operator"}}
	if got, ok := operatorReassignment([]events.Event{op, other}, "a", "claude"); !ok || got.ID != "e1" {
		t.Fatal("the operator's reassignment did not authorize its worker")
	}
	if _, ok := operatorReassignment([]events.Event{op}, "a", "agy"); ok {
		t.Fatal("an operator reassignment authorized another worker")
	}
	if _, ok := operatorReassignment([]events.Event{op, runtime}, "a", "claude"); ok {
		t.Fatal("a later runtime reassignment kept the operator's authority")
	}
	if _, ok := operatorReassignment([]events.Event{other}, "a", "claude"); ok {
		t.Fatal("another task's reassignment authorized this task")
	}
}
