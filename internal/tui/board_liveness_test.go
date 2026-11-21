//go:build linux

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// governedWorkspace is a Board on a run whose only worker is a governed
// codex process: no tmux window, only the runtime's heartbeats.
func governedWorkspace(t *testing.T, state marshal.TaskState, beats func() []app.WorkerBeat) (*Workspace, *fakeDecisions, *MarshalPanel) {
	t.Helper()
	w := NewWorkspace(nil, "project", "session")
	w.screen = NewScreen(nil)
	w.beatSource = func(runID string) []app.WorkerBeat {
		if runID != "R-80" {
			return nil
		}
		return beats()
	}
	src := &fakeDecisions{}
	src.add(events.Event{RunID: "R-80", Type: events.EventTypeMarshalPlanApproved, At: time.Now().Add(-time.Hour)})
	run := marshal.Run{State: marshal.Dispatching, PlanID: "PLAN-80", Settings: marshal.Settings{ReworkLimit: 2}, Tasks: []marshal.Task{
		{PlanTaskID: "browser-fixes", Title: "browser fixes", Worker: "codex", State: state, Files: []string{"web/app.js"}},
	}}
	p := newMarshalPanel("R-80", "codex", run, "running")
	evs, _, _, err := w.boardFeed.read(t.Context(), src, p.RunID)
	if err != nil {
		t.Fatal(err)
	}
	p.Board = w.projectBoard(p, evs)
	w.setMarshalPanel(p)
	return w, src, p
}

// Finding #79: the run said running for an hour while no worker existed.
// A run with no live worker past the grace says so in its header, from the
// last time the Board saw one alive.
func TestBoardRunHeaderSaysNoWorker(t *testing.T) {
	b := model.Board{Run: model.RunHead{ID: "R-79", State: marshal.Dispatching, Driving: true, WorkerSeen: time.Now().Add(-time.Hour)},
		Tasks: []model.TaskRow{{ID: "browser-fixes", State: model.TaskWorking, Worker: "codex"}}}
	v := boardVisual{board: b, now: time.Now()}
	if _, word, color := v.runWord(); word != "not running: no worker for 1h00m" || color != "needs" {
		t.Fatalf("runWord = %q %q", word, color)
	}
	if n := v.running(); n != 0 {
		t.Fatalf("running = %d, want 0 with no worker", n)
	}
	// Within the grace, or while the Marshal reviews, the run is running.
	v.board.Run.WorkerSeen = time.Now().Add(-30 * time.Second)
	if _, word, _ := v.runWord(); word != "running" {
		t.Fatalf("within the grace runWord = %q", word)
	}
	v.board.Run.WorkerSeen = time.Now().Add(-time.Hour)
	v.board.Tasks = append(v.board.Tasks, model.TaskRow{ID: "other", State: model.TaskReview})
	if _, word, _ := v.runWord(); strings.Contains(word, "no worker") {
		t.Fatalf("a review in progress reads %q", word)
	}
	// A board that never learned when a worker was seen judges nothing.
	v.board.Tasks, v.board.Run.WorkerSeen = v.board.Tasks[:1], time.Time{}
	if _, word, _ := v.runWord(); word != "running" {
		t.Fatalf("unknown liveness runWord = %q", word)
	}
}

// The feed counts the run's quiet time from the last live worker, and the
// Marshal's own work (a review) is not quiet.
func TestBoardFeedWorkerSeen(t *testing.T) {
	var f boardFeed
	f.runID = "R"
	now := time.Now()
	old := now.Add(-10 * time.Minute)
	lost := []model.Worker{{ID: "codex-1", TaskID: "t", Heartbeat: true, Started: old, Beat: old}}
	if _, seen := f.noteWorkers("R", nil, false, now); !seen.Equal(now) {
		t.Fatalf("first look seen = %v, want now: nothing earlier is known", seen)
	}
	f.seen = old.Add(-time.Hour)
	if _, seen := f.noteWorkers("R", lost, false, now); !seen.Equal(old) {
		t.Fatalf("seen = %v, want the lost worker's last beat %v", seen, old)
	}
	if _, seen := f.noteWorkers("R", nil, true, now); !seen.Equal(now) {
		t.Fatalf("seen = %v while the Marshal works, want now", seen)
	}
	later := now.Add(4 * time.Minute)
	changed, seen := f.noteWorkers("R", nil, false, later)
	if !changed || !seen.Equal(now) {
		t.Fatalf("changed %t seen %v, want a quiet run to repaint and keep %v", changed, seen, now)
	}
	if changed, _ := f.noteWorkers("R", nil, false, later.Add(10*time.Second)); changed {
		t.Fatal("the same quiet minute repainted again")
	}
}
