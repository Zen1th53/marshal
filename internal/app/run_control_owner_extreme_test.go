//go:build !governed

package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func runControlOwnerFixture(t *testing.T, state execution.RunState) (*Runtime, CommandEnvelope) {
	t.Helper()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	owner, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	if !owner.IsLifecycleOwner() {
		t.Fatal("fixture must hold actual kernel lifecycle lock")
	}
	goal := model.GoalContract{ID: "GOAL-owner-control", SessionID: "S-owner-control", ProjectID: owner.ProjectIdentity(), Revision: 1, OriginalRequest: "bounded owner control", DesiredOutcome: "bounded owner control", ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationApproved, Risk: model.R1, AuthoritySource: "owner"}
	if err := owner.Store().SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	runs, err := execution.NewFileRunStore(filepath.Join(owner.layout.RuntimeDir, "execution", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := execution.ExecutionRun{RunID: "RUN-owner-control", ProjectID: projectid.ID(owner.ProjectIdentity()), SessionID: goal.SessionID, GoalID: goal.ID, GoalRevision: 1, State: state, StartedAt: now, UpdatedAt: now, Tasks: map[string]execution.TaskExecution{"task-owner-control": {TaskID: "task-owner-control", CanonicalTaskID: "TASK-owner-control"}}}
	if err = runs.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	// Load the owner's production engine after its disposable durable fixture exists.
	stored, err := owner.Execution().Engine().GetRun(t.Context(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return owner, CommandEnvelope{ProjectID: owner.ProjectIdentity(), SessionID: goal.SessionID, TargetID: "run:" + run.RunID, ExpectedVersion: stored.Version, IdempotencyKey: "owner-control"}
}

func runControlExecutionSnapshot(t *testing.T, r *Runtime) map[string][32]byte {
	t.Helper()
	result := map[string][32]byte{}
	root := filepath.Join(r.layout.RuntimeDir, "execution")
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[rel] = sha256.Sum256(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
func runControlReceiptCounts(t *testing.T, r *Runtime) [2]int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+r.layout.Database+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n [2]int
	for i, table := range []string{"command_results", "command_audit"} {
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE operation LIKE 'run.%'").Scan(&n[i]); err != nil {
			t.Fatal(err)
		}
	}
	return n
}

func TestManagedRunControlForeignOwnerRefusesBeforeEffects(t *testing.T) {
	for _, operation := range []string{"pause", "resume", "cancel", "replay"} {
		t.Run(operation, func(t *testing.T) {
			state := execution.RunRunning
			if operation == "resume" {
				state = execution.RunPaused
			}
			owner, e := runControlOwnerFixture(t, state)
			ownerLocal, err := owner.OpenLocalControl(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var canceled, resumed atomic.Int32
			owner.taskRuns = map[string]context.CancelFunc{"TASK-owner-control": func() { canceled.Add(1) }}
			owner.resumeRun = func(string) { resumed.Add(1) }
			if operation == "replay" {
				if _, err = owner.CommandRunControl(ownerLocal.Context(t.Context()), e, "pause"); err != nil {
					t.Fatal(err)
				}
			}
			other, err := Open(t.Context(), owner.ProjectRoot())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Close() })
			if other.IsLifecycleOwner() {
				t.Fatal("foreign window acquired kernel lifecycle lock")
			}
			local, err := other.OpenLocalControl(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			other.resumeRun = func(string) { resumed.Add(1) }
			other.taskRuns = map[string]context.CancelFunc{"TASK-owner-control": func() { canceled.Add(1) }}
			// Initialize its cache before measuring mutation; reads/control attachments remain supported.
			if _, err := other.Execution().Engine().GetRun(t.Context(), "RUN-owner-control"); err != nil {
				t.Fatal(err)
			}
			before := runControlExecutionSnapshot(t, owner)
			counts := runControlReceiptCounts(t, owner)
			op := operation
			if op == "replay" {
				op = "pause"
			} else {
				e.IdempotencyKey = "foreign-" + operation
			}
			result, err := other.CommandRunControl(local.Context(t.Context()), e, op)
			if !errors.Is(err, ErrRuntimeOwned) {
				t.Errorf("foreign %s reported state=%s executing=%v error=%v", operation, result.Run.State, result.Executing, err)
			}
			if !reflect.DeepEqual(before, runControlExecutionSnapshot(t, owner)) {
				t.Errorf("foreign %s changed durable execution files/journal", operation)
			}
			if after := runControlReceiptCounts(t, owner); after != counts {
				t.Errorf("foreign %s changed command receipts: %v -> %v", operation, counts, after)
			}
			if canceled.Load() != 0 || resumed.Load() != 0 {
				t.Errorf("foreign %s invoked turn effects cancel=%d resume=%d", operation, canceled.Load(), resumed.Load())
			}
		})
	}
}

func TestManagedRunControlClosedOwnerRefusesBeforeEffects(t *testing.T) {
	owner, e := runControlOwnerFixture(t, execution.RunRunning)
	local, err := owner.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := owner.CommandRunControl(local.Context(t.Context()), e, "cancel")
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed owner returned %s/%v, want closed", result.Run.State, err)
	}
}

// Exercise the actual command's turn-mutation callback while Close competes,
// without starting any provider process or native/model turn.
func TestManagedRunControlSerializesSynchronousCancelWithClose(t *testing.T) {
	owner, e := runControlOwnerFixture(t, execution.RunRunning)
	local, err := owner.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	owner.taskRuns = map[string]context.CancelFunc{"TASK-owner-control": func() { close(entered); <-release }}
	done := make(chan error, 1)
	go func() { _, err := owner.CommandRunControl(local.Context(t.Context()), e, "cancel"); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("owner cancellation callback did not enter")
	}
	// The admission mutex is also Close's lease-release boundary. The callback
	// already owns taskMu, so this proves the required ownerMu -> taskMu order.
	if owner.ownerMu.TryLock() {
		owner.ownerMu.Unlock()
		close(release)
		<-done
		t.Fatal("cancel turn effects escaped the lifecycle-owner admission lock")
	}
	closeStarted, closed := make(chan struct{}), make(chan error, 1)
	go func() { close(closeStarted); closed <- owner.Close() }()
	<-closeStarted
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("owner cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner cancel deadlocked with Close")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("owner Close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not settle after synchronous cancel")
	}
	if owner.IsLifecycleOwner() {
		t.Fatal("closed owner retained lifecycle lock")
	}
}
