package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// busyRetryWorkspace is the live case: rv02 escalated and the run waits for
// the owner to retry it, while the Marshal step is still collecting rv03.
// The stop commands' handler refuses while the step runs, as marshalRetry
// and marshalResume do, and records every command that reached it.
func busyRetryWorkspace(t *testing.T) (*Workspace, *[]string, *bool) {
	t.Helper()
	orig := boardStopHandle
	t.Cleanup(func() { boardStopHandle = orig })
	var ran []string
	boardStopHandle = func(w *Workspace, _ context.Context, cmd string) (string, error) {
		ran = append(ran, cmd)
		if w.marshalBusy() {
			return "", errors.New("a Marshal operation is already running; /marshal stop first")
		}
		return "ok", nil
	}
	w := NewWorkspace(nil, "project", "session")
	w.screen = NewScreen(nil)
	t.Cleanup(w.Close)
	t.Cleanup(func() { v2Cards.Delete(w) })
	pause := marshal.Pause{
		Reason: "Issue 02: whole-thread withdrawal couldn't start: its workspace didn't match the plan.",
		Detail: "task lane admission refused: execution run conflict: provider switch requires the current task lane parent",
		Action: marshal.PauseRetryTask, TaskID: "rv02",
	}
	run := marshal.Run{
		State: marshal.AwaitingUser, PlanID: "PLAN-1", Settings: marshal.Settings{ReworkLimit: 2}, Pause: &pause,
		Tasks: []marshal.Task{
			{PlanTaskID: "rv02", Title: "Issue 02", Worker: "codex", State: marshal.Escalated, Branch: "marshal/rv02"},
			{PlanTaskID: "rv03", Title: "Issue 03", Worker: "claude", State: marshal.HandedIn, Branch: "marshal/rv03"},
		},
	}
	w.setMarshalPanel(newMarshalPanel("R-9", "codex", run, "running"))
	cancelled := false
	m := w.marshalSession()
	m.mu.Lock()
	m.busy, m.cancel = true, func() { cancelled = true }
	m.running.Store(true)
	m.mu.Unlock()
	return w, &ran, &cancelled
}

// Pressing Enter on the paused card ten times while the step runs (the
// workstation log: ten "/marshal retry rv02: a Marshal operation is already
// running" lines in three seconds) fires no retry, shows one plain line that
// does not tell the owner to stop the step, and never cancels the step. When
// the step ends, the retry and the resume run exactly once.
func TestPausedCardEnterWhileStepRunsQueuesOneRetry(t *testing.T) {
	w, ran, cancelled := busyRetryWorkspace(t)
	if _, ok := w.v2PauseNow(boardNow()); !ok {
		t.Fatal("no paused card for the escalated task")
	}
	for range 10 {
		w.handleV2CardKey(KeyEvent{Type: KeyEnter})
	}
	if len(*ran) != 0 {
		t.Fatalf("Enter during the step ran %d commands %q, want none", len(*ran), *ran)
	}
	if *cancelled {
		t.Fatal("Enter during the step cancelled the running step")
	}
	answer := w.state.LastOutput
	if !strings.Contains(answer, "retry rv02") || strings.Contains(answer, "/marshal stop") || w.state.LastOutputIsError {
		t.Fatalf("answer %q", answer)
	}
	m := w.marshalSession()
	m.finish(nil)
	if want := []string{"/marshal retry rv02", "/marshal resume"}; !slices.Equal(*ran, want) {
		t.Fatalf("after the step the queue ran %q, want %q", *ran, want)
	}
	if *cancelled {
		t.Fatal("the step was cancelled")
	}
	// Nothing stays queued: a later step's end runs nothing more.
	m.mu.Lock()
	m.busy = true
	m.mu.Unlock()
	m.finish(nil)
	if len(*ran) != 2 {
		t.Fatalf("the queued retry ran again: %q", *ran)
	}
}

// The inbox's stop entry (the classic Board's Enter) takes the same path.
func TestStopEntryEnterWhileStepRunsQueuesOneRetry(t *testing.T) {
	w, ran, cancelled := busyRetryWorkspace(t)
	stop, ok := w.stopVisual(w.marshalPanel()).stopNotice()
	if !ok {
		t.Fatal("no stop")
	}
	for range 10 {
		w.actOnStop(context.Background(), stop)
	}
	if len(*ran) != 0 || *cancelled {
		t.Fatalf("ran %q cancelled %v", *ran, *cancelled)
	}
	w.marshalSession().finish(nil)
	if want := []string{"/marshal retry rv02", "/marshal resume"}; !slices.Equal(*ran, want) {
		t.Fatalf("after the step the queue ran %q, want %q", *ran, want)
	}
}

// x (or /marshal stop) drops a queued action; and a queued action the run no
// longer asks for is dropped with a plain note instead of running.
func TestQueuedStopDroppedOnStopOrWhenRunMovedOn(t *testing.T) {
	t.Run("stop", func(t *testing.T) {
		w, ran, _ := busyRetryWorkspace(t)
		w.handleV2CardKey(KeyEvent{Type: KeyEnter})
		w.marshalStop()
		w.marshalSession().finish(nil)
		if len(*ran) != 0 {
			t.Fatalf("ran %q after stop", *ran)
		}
	})
	t.Run("moved on", func(t *testing.T) {
		w, ran, _ := busyRetryWorkspace(t)
		w.handleV2CardKey(KeyEvent{Type: KeyEnter})
		run := marshal.Run{State: marshal.Dispatching, PlanID: "PLAN-1", Tasks: []marshal.Task{{PlanTaskID: "rv02", Worker: "codex", State: marshal.Dispatched}}}
		w.setMarshalPanel(newMarshalPanel("R-9", "codex", run, "running"))
		w.marshalSession().finish(nil)
		if len(*ran) != 0 || !strings.Contains(w.state.LastOutput, "did not run") {
			t.Fatalf("ran %q answer %q", *ran, w.state.LastOutput)
		}
	})
}
