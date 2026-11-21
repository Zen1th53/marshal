//go:build linux

package tui

import (
	"context"
	"slices"
	"strings"
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

// Finding 75: after a restart the run showed "reviewing · 0/1" with an empty
// inbox and no action, and only `/marshal retry browser-fixes fresh-session`
// then `/marshal resume` moved it on. The Board offers that next step as the
// inbox's stop entry, and Enter runs those same commands.
func TestStalledRunOffersRetryAndResume(t *testing.T) {
	// This test is about the classic Board itself.
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
