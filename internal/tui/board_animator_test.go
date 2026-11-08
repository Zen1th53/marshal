package tui

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

func boardPanel(state boardmodel.TaskState) *MarshalPanel {
	b := boardmodel.Board{Tasks: []boardmodel.TaskRow{{ID: "T-1", State: state}}}
	// A moving task has its live lane; without one it would be stalled.
	if lane, ok := map[boardmodel.TaskState]boardmodel.LaneState{boardmodel.TaskWorking: boardmodel.LaneWorking, boardmodel.TaskReview: boardmodel.LaneReview}[state]; ok {
		b.Lanes = []boardmodel.Lane{{Worker: "codex-1", Provider: "codex", TaskID: "T-1", State: lane}}
	}
	return &MarshalPanel{RunID: "R1", Board: b}
}

func drainRepaints(w *Workspace, within time.Duration) int {
	n := 0
	deadline := time.After(within)
	for {
		select {
		case <-w.repaint:
			n++
		case <-deadline:
			return n
		}
	}
}

// The workspace loop draws a frame only on a repaint, a UI event, a resize or
// a key, so an idle Board that asks for no repaint writes no frame. The one
// repaint is the publish itself, a state change.
func TestIdleMarshalBoardAsksForNoFrames(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.startAnimator()
	defer w.stopAnimator()
	w.setMarshalPanel(boardPanel(boardmodel.TaskQueued))
	if n := drainRepaints(w, 8*anim.Tick); n != 1 {
		t.Fatalf("idle publish asked for %d repaints, want the publish's 1", n)
	}
	if w.animator.Load().Running() {
		t.Fatal("idle Board started the animator")
	}
	if n := drainRepaints(w, 8*anim.Tick); n != 0 {
		t.Fatalf("idle Board asked for %d repaints", n)
	}
}

func TestWorkingMarshalBoardAnimatesUntilItStops(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.setMarshalPanel(boardPanel(boardmodel.TaskWorking))
	w.startAnimator()
	a := w.animator.Load()
	if !a.Running() {
		t.Fatal("a panel already working when the screen opened did not animate")
	}
	if n := drainRepaints(w, 8*anim.Tick); n < 3 {
		t.Fatalf("working Board asked for %d repaints in %v", n, 8*anim.Tick)
	}
	w.setMarshalPanel(nil)
	if a.Running() {
		t.Fatal("clearing the panel left the animator running")
	}
	drainRepaints(w, 2*anim.Tick)
	if n := drainRepaints(w, 4*anim.Tick); n != 0 {
		t.Fatalf("%d repaints after the Board was cleared", n)
	}
	w.setMarshalPanel(boardPanel(boardmodel.TaskWorking))
	w.stopAnimator()
	if a.Running() || w.animator.Load() != nil {
		t.Fatal("leaving the raw terminal left the animator")
	}
	// Publishing with no screen must not start anything.
	w.setMarshalPanel(boardPanel(boardmodel.TaskWorking))
	if a.Running() {
		t.Fatal("a publish after the screen closed restarted the animator")
	}
}
