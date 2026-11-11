package model

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// A task in review has no worker lane: while this process drives the run the
// Marshal is reviewing it, so it is not stalled. Without a driver it still is,
// and a working task with no lane is stalled either way.
func TestReviewIsNotStalledWhileTheRunIsDriven(t *testing.T) {
	r := run(task("T-1", marshal.HandedIn), task("T-2", marshal.Dispatched))
	driven := Project(Snapshot{RunID: "R1", Run: r, Driving: true}, nil)
	idle := Project(Snapshot{RunID: "R1", Run: r}, nil)
	row := func(b Board, id string) TaskRow {
		for _, t := range b.Tasks {
			if t.ID == id {
				return t
			}
		}
		t.Fatalf("no row %s", id)
		return TaskRow{}
	}
	if driven.Stalled(row(driven, "T-1")) {
		t.Fatal("a hand-in the driven run is reviewing is shown stalled")
	}
	if !idle.Stalled(row(idle, "T-1")) {
		t.Fatal("a hand-in nothing reviews is not shown stalled")
	}
	if !driven.Stalled(row(driven, "T-2")) {
		t.Fatal("a working task with no live worker is not shown stalled")
	}
}

// A finished task's time is frozen at how long it took, from its first
// dispatch to its merge; a live task's time runs from when it entered its
// state.
func TestFinishedTaskElapsedIsFrozen(t *testing.T) {
	r := run(task("T-1", marshal.Merged), task("T-2", marshal.Dispatched), task("T-3", marshal.Cancelled))
	b := Project(Snapshot{RunID: "R1", Run: r}, []events.Event{
		ev(10, events.EventTypeMarshalTaskDispatched, "T-1", nil),
		ev(20, events.EventTypeMarshalTaskDispatched, "T-2", nil),
		ev(70, events.EventTypeMarshalTaskReturned, "T-1", nil),
		ev(80, events.EventTypeMarshalTaskDispatched, "T-1", nil),
		ev(130, events.EventTypeMarshalTaskMerged, "T-1", nil),
		ev(140, events.EventTypeMarshalTaskCancelled, "T-3", nil),
	})
	rows := map[string]TaskRow{}
	for _, t := range b.Tasks {
		rows[t.ID] = t
	}
	for _, later := range []time.Duration{0, time.Minute, time.Hour} {
		now := epoch.Add(130*time.Second + later)
		if d, ok := rows["T-1"].Elapsed(now); !ok || d != 120*time.Second {
			t.Fatalf("merged task at +%s: elapsed %s %v, want 2m0s frozen", later, d, ok)
		}
		if d, ok := rows["T-2"].Elapsed(now); !ok || d != 110*time.Second+later {
			t.Fatalf("working task at +%s: elapsed %s %v, want it running", later, d, ok)
		}
		if d, ok := rows["T-3"].Elapsed(now); ok {
			t.Fatalf("a cancelled task that never ran has a time: %s", d)
		}
	}
}
