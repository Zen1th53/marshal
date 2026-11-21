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

// Finding #80: a governed codex worker alive for 18 minutes showed as
// "LANES idle · 0 running" and its task as rework. Its heartbeats now make
// it a running lane, with its elapsed time and the age of its last beat.
func TestBoardLaneFollowsGovernedWorkerHeartbeat(t *testing.T) {
	// This test is about the classic Board itself.
	started := time.Now().Add(-18 * time.Minute)
	beat := time.Now().Add(-12 * time.Second)
	w, _, p := governedWorkspace(t, marshal.Returned, func() []app.WorkerBeat {
		return []app.WorkerBeat{{RunID: "R-80", TaskID: "browser-fixes", Worker: "codex", Started: started, Beat: beat}}
	})
	b := p.Board
	if len(b.Lanes) != 1 || b.Lanes[0].Worker != "codex-1" || b.Lanes[0].State != model.LaneWorking || !b.Lanes[0].Heartbeat {
		t.Fatalf("lanes = %+v, want one working heartbeat lane codex-1", b.Lanes)
	}
	if b.Tasks[0].State != model.TaskWorking || b.Run.Running != 1 || b.Run.ActiveLanes != 1 {
		t.Fatalf("task %s, running %d, lanes %d; want the beating worker's task working", b.Tasks[0].State, b.Run.Running, b.Run.ActiveLanes)
	}
	frame := plainFrame(w, 140, 40)
	for _, want := range []string{"1 running", "codex-1", "18:0", "last signal 12s"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame lacks %q:\n%s", want, frame)
		}
	}
	for _, bad := range []string{"LANES idle", "stalled", "no worker"} {
		if strings.Contains(frame, bad) {
			t.Fatalf("frame says %q while the worker beats:\n%s", bad, frame)
		}
	}
}

// A worker that stops beating for longer than the grace is no longer
// running: the lane says no worker is running its task, the run header stops
// saying running, and the poll notices it without any new decision.
func TestBoardLaneLostWhenHeartbeatsStop(t *testing.T) {
	// This test is about the classic Board itself.
	started := time.Now().Add(-20 * time.Minute)
	beat := time.Now()
	w, src, p := governedWorkspace(t, marshal.Dispatched, func() []app.WorkerBeat {
		return []app.WorkerBeat{{RunID: "R-80", TaskID: "browser-fixes", Worker: "codex", Started: started, Beat: beat}}
	})
	if p.Board.Lanes[0].State != model.LaneWorking {
		t.Fatalf("lane = %+v, want working", p.Board.Lanes[0])
	}
	// Five minutes pass without a beat.
	later := time.Now().Add(5 * time.Minute)
	boardNow = func() time.Time { return later }
	t.Cleanup(func() { boardNow = time.Now })
	if !w.refreshBoardFrom(t.Context(), src, p) {
		t.Fatal("a worker gone quiet did not repaint the Board")
	}
	b := w.marshalPanel().Board
	if b.Lanes[0].State != model.LaneLost || b.Live("browser-fixes") || b.Run.ActiveLanes != 0 {
		t.Fatalf("lane = %+v active %d, want lost", b.Lanes[0], b.Run.ActiveLanes)
	}
	frame := plainFrame(w, 180, 40)
	for _, want := range []string{"no worker is running this task", "/marshal stop, then /marshal resume", "not running: no worker for 5m", "no worker is running"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame lacks %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "› running") || strings.Contains(frame, "1 running") || strings.Contains(frame, "· running ·") {
		t.Fatalf("frame still says running:\n%s", frame)
	}
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
