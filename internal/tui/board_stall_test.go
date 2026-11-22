//go:build linux

package tui

import (
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
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
