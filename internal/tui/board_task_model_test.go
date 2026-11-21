//go:build linux

package tui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// seedTaskCandidates replaces the task-model candidate seam with a fixed list
// for the test and restores it afterwards.
func seedTaskCandidates(t *testing.T, cands []pickerCand) {
	t.Helper()
	old := boardTaskCandidates
	boardTaskCandidates = func(*Workspace, context.Context) []pickerCand { return cands }
	t.Cleanup(func() { boardTaskCandidates = old })
}

// fourModels is the task-model picker's candidate rows in driver order.
func fourModels() []pickerCand {
	return []pickerCand{
		{provider: "codex", left: 50, known: true},
		{provider: "claude", left: 90, known: true},
		{provider: "opencode", left: 70, known: true},
		{provider: "agy", left: 30, known: true},
	}
}

func TestTaskModelPickerApplies(t *testing.T) {
	orig := boardMove
	defer func() { boardMove = orig }()
	ctx := t.Context()

	move := func(t *testing.T, id string) []string {
		w, _ := boardWorkspace(t)
		defer w.Close()
		seedTaskCandidates(t, fourModels())
		var calls []string
		boardMove = func(w *Workspace, ctx context.Context, id, worker string) (string, error) {
			calls = append(calls, "reassign "+id+" "+worker)
			return moveContinuation(w, id, worker, w.taskWaitsForRework(id), func() error {
				calls = append(calls, "resume")
				return nil
			}), nil
		}
		w.openTaskModelPicker(ctx, id)
		if !w.marshalPickerOpen() {
			t.Fatalf("%s did not open the picker", id)
		}
		w.clickMarshalPicker(ctx, "claude")
		w.commandWG.Wait()
		return calls
	}

	// A parked task is reassigned and the run resumed.
	if got := move(t, "T-023"); !slicesEqualStr(got, []string{"reassign T-023 claude", "resume"}) {
		t.Fatalf("T-023 calls %q, want reassign then resume", got)
	}

	// A queued task (shipped build only) is reassigned without a resume: the
	// run already moving picks it up.
	if app.OperatorCanMoveQueuedTask() {
		if got := move(t, "T-033"); !slicesEqualStr(got, []string{"reassign T-033 claude"}) {
			t.Fatalf("T-033 calls %q, want reassign only", got)
		}
	}
}

func TestTaskModelQueuedWhileBusy(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	seedTaskCandidates(t, fourModels())
	ctx := t.Context()
	m := w.marshalSession()
	m.mu.Lock()
	m.busy = true
	m.mu.Unlock()

	// Choosing a model for T-023 while a step runs queues the move.
	w.openTaskModelPicker(ctx, "T-023")
	w.clickMarshalPicker(ctx, "claude")
	m.mu.Lock()
	queued := m.nextTaskMoves["T-023"]
	m.mu.Unlock()
	if queued != "claude" {
		t.Fatalf("queued move %q, want claude", queued)
	}
	if got, want := pickerLastOutput(w), "The Marshal is mid-step. T-023 moves to Claude Code when that step ends — unless it has started by then."; got != want {
		t.Fatalf("queue answer %q, want %q", got, want)
	}

	// The task starts before the step ends: the drain drops the move and says
	// so, leaving its model unchanged.
	w.mu.Lock()
	for i := range w.state.Marshal.Board.Tasks {
		if w.state.Marshal.Board.Tasks[i].ID == "T-023" {
			w.state.Marshal.Board.Tasks[i].State = boardmodel.TaskWorking
		}
	}
	w.mu.Unlock()
	// While the step is still running the drain takes nothing off the queue.
	w.drainTaskMoves(ctx)
	m.mu.Lock()
	still := m.nextTaskMoves["T-023"]
	m.busy = false // the step ends; onIdle drains now
	m.mu.Unlock()
	if still != "claude" {
		t.Fatalf("a drain during the step removed the queued move")
	}
	w.drainTaskMoves(ctx)
	if got, want := pickerLastOutput(w), "T-023 already started on Codex; its model was not changed."; got != want {
		t.Fatalf("drain answer %q, want %q", got, want)
	}
	m.mu.Lock()
	left := len(m.nextTaskMoves)
	m.mu.Unlock()
	if left != 0 {
		t.Fatalf("drain left %d queued moves", left)
	}
}

// TestTaskModelQueuedMovesAllApply: two moves queued during one step both run
// after it, one at a time on the command lane; none is lost to the busy lane.
func TestTaskModelQueuedMovesAllApply(t *testing.T) {
	orig := boardMove
	defer func() { boardMove = orig }()
	w, _ := boardWorkspace(t)
	defer w.Close()
	ctx := t.Context()
	var mu sync.Mutex
	var calls []string
	boardMove = func(_ *Workspace, _ context.Context, id, worker string) (string, error) {
		mu.Lock()
		calls = append(calls, id+" "+worker)
		mu.Unlock()
		return id + " will run on " + worker + ".", nil
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.nextTaskMoves = map[string]string{"T-033": "claude", "T-023": "opencode"}
	m.mu.Unlock()

	want := []string{"T-023 opencode", "T-033 claude"}
	w.drainTaskMoves(ctx)
	// Each finished command drains the next move; wait until both have run.
	deadline := time.Now().Add(10 * time.Second)
	for {
		w.commandWG.Wait()
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if n >= len(want) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slicesEqualStr(calls, want) {
		t.Fatalf("queued moves ran %q, want %q", calls, want)
	}
}

func TestPlainMoveError(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"same harness", errors.New("cannot reassign to the same harness"), "T-023 already runs on Claude Code."},
		{"not escalated", errors.New("only escalated tasks can be reassigned"), "T-023 cannot move right now; it can once it gets stuck."},
		{"governed plan", errors.New("task T-023 is governed by its approved plan, which assigns worker codex; switching it to claude needs a plan amendment (/marshal amend)"), "T-023's model is set by the approved plan; change the plan to change it."},
		{"task mode", errors.New("worker claude does not support task mode governed"), "Claude Code cannot run this kind of task."},
		{"escalation reason", errors.New("cannot reassign task with escalation reason: budget exceeded"), "T-023 cannot move: budget exceeded."},
		{"other", errors.New("the store rejected a revisioned save"), "T-023 could not move right now."},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := w.plainMoveError("T-023", "claude", c.err); got != c.want {
				t.Fatalf("plainMoveError %q, want %q", got, c.want)
			}
		})
	}
}

func slicesEqualStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
