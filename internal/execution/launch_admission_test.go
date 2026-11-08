package execution

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
)

type lockOrderGoalReader struct {
	*store.Store
	engine      *Engine
	sawUnlocked atomic.Bool
}

func (r *lockOrderGoalReader) BeginGoalDispatch(ctx context.Context, session, goal string, revision int64) (func(), error) {
	if r.engine.operationMu.TryLock() {
		r.sawUnlocked.Store(true)
		r.engine.operationMu.Unlock()
	}
	return r.Store.BeginGoalDispatch(ctx, session, goal, revision)
}
func TestDispatchAcquiresOperationLockBeforeSQLite(t *testing.T) {
	e, s, run := budgetDispatchEngine(t, nil)
	reader := &lockOrderGoalReader{Store: s, engine: e}
	e.goalReader = reader
	if _, err := e.dispatchBudgetedTask(t.Context(), run, TaskExecution{TaskID: "ordered", Attempts: 1}, ConstraintPackage{}, "", NewMockHarness("ordered", nil)); err != nil {
		t.Fatal(err)
	}
	if reader.sawUnlocked.Load() {
		t.Fatal("SQLite admission began before operationMu: dispatch can deadlock with resume")
	}
}

func TestDispatchAdmissionRemainsHeldUntilBackendAcknowledgement(t *testing.T) {
	e, s, run := budgetDispatchEngine(t, nil)
	preparing := make(chan struct{})
	continueLaunch := make(chan struct{})
	admitted := make(chan struct{})
	acknowledge := make(chan struct{})
	workerDone := make(chan struct{})
	done := make(chan error, 1)
	h := NewCodexNativeHarnessWithExecutor("", func(ctx context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		// This SQLite read must finish before admission takes the single connection.
		if _, err := s.GetActiveGoalContract(ctx, "s"); err != nil {
			return TaskResult{}, err
		}
		close(preparing)
		<-continueLaunch
		release, err := BeginWorkerLaunch(ctx)
		if err != nil {
			return TaskResult{}, err
		}
		defer release()
		close(admitted)
		<-acknowledge
		release()
		<-workerDone
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	})
	go func() {
		_, err := e.dispatchBudgetedTask(t.Context(), run, TaskExecution{TaskID: "ack", Attempts: 1}, ConstraintPackage{}, "", h)
		done <- err
	}()
	select {
	case <-preparing:
	case <-time.After(time.Second):
		t.Fatal("preparation blocked on admission")
	}
	close(continueLaunch)
	select {
	case <-admitted:
	case <-time.After(time.Second):
		t.Fatal("launch did not reach admission")
	}
	edited := make(chan error, 1)
	go func() { _, err := s.CancelGoalContract(t.Context(), "s", "g", 1, "stop"); edited <- err }()
	select {
	case err := <-edited:
		t.Fatalf("goal changed before backend acknowledgement: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(acknowledge)
	select {
	case err := <-edited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("goal edit blocked after launch acknowledgement")
	}
	close(workerDone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
