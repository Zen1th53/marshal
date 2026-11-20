package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// stalledWorkspace is the Board after a restart (finding 75): the run is
// reviewing, its one task was returned, nothing works on it, the inbox is
// empty and no Marshal step runs in this window.
func stalledWorkspace(t *testing.T, state marshal.TaskState) *Workspace {
	t.Helper()
	w := NewWorkspace(nil, "project", "session")
	w.screen = NewScreen(nil)
	run := marshal.Run{
		State:    marshal.Reviewing,
		PlanID:   "PLAN-1",
		Settings: marshal.Settings{ReworkLimit: 2},
		Tasks: []marshal.Task{
			{PlanTaskID: "browser-fixes", Title: "browser fixes", Worker: "codex", State: state, Files: []string{"web/app.js"}},
		},
	}
	w.setMarshalPanel(newMarshalPanel("R-9", "codex", run, "stored run recovered"))
	t.Cleanup(w.Close)
	return w
}

// Finding 75: after a restart the run showed "reviewing · 0/1" with an empty
// inbox and no action, and only `/marshal retry browser-fixes fresh-session`
// then `/marshal resume` moved it on. The Board offers that next step as the
// inbox's stop entry, and Enter runs those same commands.
func TestStalledRunOffersRetryAndResume(t *testing.T) {
	orig := boardStopHandle
	t.Cleanup(func() { boardStopHandle = orig })
	var ran []string
	boardStopHandle = func(_ *Workspace, _ context.Context, cmd string) (string, error) {
		ran = append(ran, cmd)
		return "ok", nil
	}
	w := stalledWorkspace(t, marshal.Returned)
	frame := plainFrame(w, 160, 40)
	if !strings.Contains(frame, "browser-fixes was returned and nothing is working on it") {
		t.Fatalf("the Board does not say why the run waits:\n%s", frame)
	}
	if !w.boardStopWaiting() {
		t.Fatal("the stop entry is not offered")
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) || !w.boardUI.stopArmed {
		t.Fatal("Enter did not arm the next step")
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("the second Enter was not taken")
	}
	want := []string{"/marshal retry browser-fixes fresh-session", "/marshal resume"}
	if !slices.Equal(ran, want) {
		t.Fatalf("ran %q, want %q", ran, want)
	}
}

// A reassigned task only needs the run resumed: RetryTask refuses it.
func TestStalledRunWithAMovedTaskOffersResume(t *testing.T) {
	w := stalledWorkspace(t, marshal.Reassigned)
	stop, ok := w.stopVisual(w.marshalPanel()).stopNotice()
	if !ok || !slices.Equal(stop.commands, []string{"/marshal resume"}) {
		t.Fatalf("stop = %+v, %v; want a resume", stop, ok)
	}
}

// A run that is moving is never called stalled: a Marshal step runs in this
// window, or a task is being worked on.
func TestMovingRunIsNotStalled(t *testing.T) {
	w := stalledWorkspace(t, marshal.Returned)
	m := w.marshalSession()
	m.running.Store(true)
	if _, ok := w.stopVisual(w.marshalPanel()).stopNotice(); ok {
		t.Fatal("a run with a Marshal step running was offered a retry")
	}
	m.running.Store(false)

	b, _ := boardWorkspace(t)
	defer b.Close()
	if _, ok := b.stopVisual(b.marshalPanel()).stopNotice(); ok {
		t.Fatal("a run with tasks being worked on was called stalled")
	}
}

// The inspector of a returned task offers the retry; r there arms it.
func TestReturnedTaskInspectorOffersRetry(t *testing.T) {
	w := stalledWorkspace(t, marshal.Returned)
	w.boardUI.task, w.boardUI.inspect = "browser-fixes", "browser-fixes"
	frame := plainFrame(w, 160, 40)
	if !strings.Contains(frame, "[r] retry this task") {
		t.Fatalf("the inspector offers no retry:\n%s", frame)
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'r'}) || w.boardUI.retryArmed != "browser-fixes" {
		t.Fatalf("r in the inspector did not arm the retry: %q", w.boardUI.retryArmed)
	}
}

// Finding 74: the menu listed "stuck task: retry it, r" and r on a ↻ task
// opened the composer and sent "r" to the Marshal. r on a returned task
// arms the retry; where r cannot retry the Board says why, and the r never
// reaches the composer.
func TestRetryKeyNeverTypesIntoTheComposer(t *testing.T) {
	w := stalledWorkspace(t, marshal.Returned)
	w.boardUI.focus, w.boardUI.task = layout.Tasks, "browser-fixes"
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'r'}) || w.boardUI.retryArmed != "browser-fixes" {
		t.Fatalf("r on a returned task did not arm its retry: %q", w.boardUI.retryArmed)
	}
	w.disarmRetry()

	b, _ := boardWorkspace(t)
	defer b.Close()
	b.permissions.mu.Lock()
	b.permissions.inbox = nil
	b.permissions.mu.Unlock()
	for _, c := range []struct {
		name          string
		focus         layout.PaneID
		task, inspect string
		want          string
	}{
		{"working task", layout.Tasks, "T-017", "", "This task is not stuck: T-017 is being worked on."},
		{"queued task", layout.Tasks, "T-033", "", "This task is not stuck: T-033 has not started yet."},
		{"another pane", layout.Lanes, "T-023", "", "r retries a stuck task: press t for the tasks and highlight one first."},
		{"working task in the inspector", "", "T-017", "T-017", "This task is not stuck: T-017 is being worked on."},
	} {
		t.Run(c.name, func(t *testing.T) {
			b.boardUI.focus, b.boardUI.task, b.boardUI.inspect = c.focus, c.task, c.inspect
			if !b.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'r'}) {
				t.Fatal("r fell through to the composer")
			}
			if b.composing() || b.boardUI.retryArmed != "" {
				t.Fatalf("composing=%v armed=%q", b.composing(), b.boardUI.retryArmed)
			}
			if got := lastOutput(b); !strings.HasPrefix(got, c.want) {
				t.Fatalf("answer %q, want %q", got, c.want)
			}
			// The menu's entry is off with the same reason.
			k, _ := boardAction("task.retry")
			if on, why := k.available(b); on || why != lastOutput(b) {
				t.Fatalf("menu entry on=%v why=%q, want off with %q", on, why, lastOutput(b))
			}
		})
	}
}
