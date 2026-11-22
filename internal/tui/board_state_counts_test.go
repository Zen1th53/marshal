//go:build linux

package tui

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// While this process drives the run, a hand-in the Marshal is reviewing is
// in review, not stalled; once the run is no longer driven (the operation
// ended, the run stopped or waits for the person) the same row is stalled.
func TestReviewRowIsNotStalledWhileTheMarshalDrivesTheRun(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	run := marshal.Run{State: marshal.Reviewing, Tasks: []marshal.Task{{PlanTaskID: "calc-add", Worker: "codex", State: marshal.HandedIn}}}
	stalled := func() bool {
		p := newMarshalPanel("RUN-1", "codex", run, "running")
		b := w.projectBoard(p, nil)
		return b.Stalled(b.Tasks[0])
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.runID, m.busy = "RUN-1", true
	m.mu.Unlock()
	if stalled() {
		t.Fatal("a hand-in under review by the driven run is shown stalled")
	}
	run.State = marshal.AwaitingUser
	if !stalled() {
		t.Fatal("a hand-in of a run waiting for the person is not shown stalled")
	}
	run.State = marshal.Reviewing
	m.mu.Lock()
	m.busy = false
	m.mu.Unlock()
	if !stalled() {
		t.Fatal("a hand-in nothing drives is not shown stalled")
	}
}
