package model

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func heartbeatSnapshot(state marshal.TaskState, beat, now time.Time) Snapshot {
	return Snapshot{
		RunID: "R", Now: now,
		Run: marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{
			{PlanTaskID: "t1", Worker: "codex", State: state},
			{PlanTaskID: "t2", Worker: "codex", State: marshal.Merged},
		}},
		Workers: []Worker{
			{ID: "codex-1", Provider: "codex", TaskID: "t1", Heartbeat: true, Started: now.Add(-18 * time.Minute), Beat: beat},
		},
	}
}

// A governed worker that beats is a working lane, and its task is working
// even when an older snapshot still says rework.
func TestProjectHeartbeatWorkerIsWorking(t *testing.T) {
	now := time.Now()
	b := Project(heartbeatSnapshot(marshal.Returned, now.Add(-12*time.Second), now), nil)
	l := b.Lanes[0]
	if l.State != LaneWorking || !l.Heartbeat || !l.Started.Equal(now.Add(-18*time.Minute)) || !b.Live("t1") {
		t.Fatalf("lane = %+v", l)
	}
	var row TaskRow
	for _, r := range b.Tasks {
		if r.ID == "t1" {
			row = r
		}
	}
	if row.State != TaskWorking || !row.Since.Equal(l.Started) || b.Stalled(row) {
		t.Fatalf("task = %+v, want working since the attempt began", row)
	}
	if b.Run.Running != 1 || b.Run.ActiveLanes != 1 || b.Run.Done != 1 {
		t.Fatalf("run = %+v", b.Run)
	}
}

// A worker silent for longer than WorkerBeatGrace is lost: not live, not an
// active lane, and its task keeps its own state.
func TestProjectHeartbeatWorkerLost(t *testing.T) {
	now := time.Now()
	b := Project(heartbeatSnapshot(marshal.Returned, now.Add(-WorkerBeatGrace-time.Second), now), nil)
	if l := b.Lanes[0]; l.State != LaneLost || b.Live("t1") || b.Run.ActiveLanes != 0 {
		t.Fatalf("lane = %+v active %d, want lost", l, b.Run.ActiveLanes)
	}
	for _, r := range b.Tasks {
		if r.ID == "t1" && r.State != TaskRework {
			t.Fatalf("lost worker moved its task to %s", r.State)
		}
	}
	// A working task with a lost worker is stalled.
	b = Project(heartbeatSnapshot(marshal.Dispatched, now.Add(-time.Hour), now), nil)
	for _, r := range b.Tasks {
		if r.ID == "t1" && !b.Stalled(r) {
			t.Fatal("a working task whose worker is lost is not stalled")
		}
	}
	// A finished task stays finished, whatever beats on it.
	s := heartbeatSnapshot(marshal.Merged, now, now)
	for _, r := range Project(s, nil).Tasks {
		if r.State != TaskMerged {
			t.Fatalf("a beating worker moved merged %s to %s", r.ID, r.State)
		}
	}
	// Without a clock nothing is judged lost.
	if b := Project(heartbeatSnapshot(marshal.Dispatched, now.Add(-time.Hour), time.Time{}), nil); b.Lanes[0].State != LaneWorking {
		t.Fatalf("zero Now judged the lane %s", b.Lanes[0].State)
	}
}
