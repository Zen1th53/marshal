package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// livenessClock is a movable clock for the worker registry.
type livenessClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *livenessClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *livenessClock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func clockedRuntime() (*Runtime, *livenessClock) {
	c := &livenessClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	r := &Runtime{}
	r.workerBeats.now = c.Now
	return r, c
}

func onlyLiveness(t *testing.T, r *Runtime, runID string) WorkerLiveness {
	t.Helper()
	ls := r.WorkerLiveness(runID)
	if len(ls) != 1 {
		t.Fatalf("liveness = %+v", ls)
	}
	return ls[0]
}

// A worker whose process never beats, or stops beating, is unverifiable:
// silence is never read as an exit. Only the attempt's own end is.
func TestMissingHeartbeatIsUnverifiable(t *testing.T) {
	r, clock := clockedRuntime()
	ctx, untrack := r.trackWorker(t.Context(), "R", "T-1", "codex")
	if l := onlyLiveness(t, r, "R"); l.Process != ProcessUnverifiable || l.Turn != TurnUnknown || l.Stale || l.Note == "" || l.AttemptID == "" {
		t.Fatalf("before any beat: %+v", l)
	}
	clock.Add(WorkerSignalGrace + time.Second)
	if l := onlyLiveness(t, r, "R"); l.Process != ProcessUnverifiable || !l.Stale {
		t.Fatalf("no beat past grace: %+v", l)
	}
	noteWorkerBeat(ctx)
	if l := onlyLiveness(t, r, "R"); l.Process != ProcessLive || l.Stale || l.Turn != TurnUnknown || l.LastSignalAt == nil || !l.LastSignalAt.Equal(clock.Now()) {
		t.Fatalf("after a beat: %+v", l)
	}
	// The heartbeat is suspended while the worker lives.
	clock.Add(WorkerSignalGrace + time.Minute)
	suspended := onlyLiveness(t, r, "R")
	if suspended.Process != ProcessUnverifiable || !suspended.Stale || suspended.Turn != TurnUnknown || suspended.Note == "" {
		t.Fatalf("suspended heartbeat: %+v", suspended)
	}
	// Then it exits.
	untrack()
	exited := onlyLiveness(t, r, "R")
	if exited.Process != ProcessExited || exited.Turn != TurnGone || exited.Stale || exited.AttemptID != suspended.AttemptID {
		t.Fatalf("exited: %+v", exited)
	}
	// After a restart nothing of the attempt is held: the dispatched task
	// stays unverifiable, never exited.
	fresh, _ := clockedRuntime()
	run := marshal.Run{Tasks: []marshal.Task{{PlanTaskID: "T-1", State: marshal.Dispatched}, {PlanTaskID: "T-2", State: marshal.Queued}}}
	got := fresh.RunLiveness("R", run)
	if len(got) != 1 || got[0].TaskID != "T-1" || got[0].Process != ProcessUnverifiable || got[0].Turn != TurnUnknown || got[0].Note == "" {
		t.Fatalf("after restart: %+v", got)
	}
	// Ended attempts are bounded.
	for range endedAttemptsKept + 5 {
		_, end := r.trackWorker(t.Context(), "R", "T-x", "codex")
		end()
	}
	if n := len(r.WorkerLiveness("R")); n > endedAttemptsKept {
		t.Fatalf("%d ended attempts kept", n)
	}
	clock.Add(endedAttemptTTL + time.Minute)
	if ls := r.WorkerLiveness("R"); len(ls) != 0 {
		t.Fatalf("ended attempts not pruned: %+v", ls)
	}
}

// A heartbeat or stream signal carried by an earlier attempt's context does
// not vouch for the attempt that replaced it on the same task.
func TestHeartbeatFromPriorAttemptIgnored(t *testing.T) {
	r, clock := clockedRuntime()
	prior, endPrior := r.trackWorker(t.Context(), "R", "T-1", "codex")
	noteWorkerBeat(prior)
	endPrior()
	clock.Add(time.Second)
	current, endCurrent := r.trackWorker(t.Context(), "R", "T-1", "codex")
	defer endCurrent()
	clock.Add(time.Second)
	noteWorkerBeat(prior)
	noteWorkerSignal(prior, adapter.SignalRequestStart)
	ls := r.WorkerLiveness("R")
	if len(ls) != 2 {
		t.Fatalf("liveness = %+v", ls)
	}
	var cur, old WorkerLiveness
	for _, l := range ls {
		if l.Process == ProcessExited {
			old = l
		} else {
			cur = l
		}
	}
	if cur.Process != ProcessUnverifiable || cur.ModelWaitSince != nil || cur.AttemptID == old.AttemptID {
		t.Fatalf("prior attempt's beat counted: current %+v prior %+v", cur, old)
	}
	if beats := r.WorkerBeats("R"); len(beats) != 1 || !beats[0].Beat.Equal(beats[0].Started) {
		t.Fatalf("prior beat renewed the current attempt: %+v", beats)
	}
	noteWorkerBeat(current)
	if l := r.RunLiveness("R", marshal.Run{Tasks: []marshal.Task{{PlanTaskID: "T-1", State: marshal.Dispatched}}}); len(l) != 1 || l[0].Process != ProcessLive {
		t.Fatalf("current attempt: %+v", l)
	}
}

// A model wait starts at the provider request, ends at its first output,
// and is announced once per attempt and wait under a stable identity.
func TestModelWaitSignalledOncePerWait(t *testing.T) {
	r, clock := clockedRuntime()
	ctx, untrack := r.trackWorker(context.Background(), "R", "T-1", "codex")
	defer untrack()
	bindWorkerAttempt(ctx, execution.TaskExecution{RunID: "EXEC-1", TaskID: "TASK-1", Attempts: 2})
	request := adapter.Request{}
	beatWorkerRequest(ctx, &request)
	request.Heartbeat()
	if l := onlyLiveness(t, r, "R"); l.Turn != TurnUnknown || l.AttemptID != "EXEC-1/TASK-1#2" {
		t.Fatalf("no instrumented start: %+v", l)
	}
	request.Progress(adapter.SignalNone) // output before a start says nothing
	request.Progress(adapter.SignalRequestStart)
	start := clock.Now()
	clock.Add(time.Minute)
	request.Heartbeat()
	if l := onlyLiveness(t, r, "R"); l.Turn != TurnWorking || l.ModelWaitSince == nil || !l.ModelWaitSince.Equal(start) || l.LastAction != "model request" {
		t.Fatalf("waiting: %+v", l)
	}
	request.Progress(adapter.SignalNone)
	if l := onlyLiveness(t, r, "R"); l.ModelWaitSince != nil || l.LastAction != "output" {
		t.Fatalf("progress did not clear the wait: %+v", l)
	}
	// While another tool call still runs, a tool's end is no model wait.
	request.Progress(adapter.SignalToolStart)
	request.Progress(adapter.SignalToolStart)
	request.Progress(adapter.SignalToolEnd)
	if l := onlyLiveness(t, r, "R"); l.ModelWaitSince != nil {
		t.Fatalf("wait with a tool in flight: %+v", l)
	}
	request.Progress(adapter.SignalToolEnd)
	clock.Add(ModelWaitNoticeAfter)
	request.Heartbeat()
	l := onlyLiveness(t, r, "R")
	if l.ModelWaitSince == nil || l.Note == "" {
		t.Fatalf("long wait: %+v", l)
	}
	request.Progress(adapter.SignalDone)
	if l := onlyLiveness(t, r, "R"); l.Turn != TurnAtRest || l.ModelWaitSince != nil {
		t.Fatalf("turn done: %+v", l)
	}
	if a, b := modelWaitNoticeID("R", "EXEC-1/TASK-1#2", 1), modelWaitNoticeID("R", "EXEC-1/TASK-1#2", 1); a != b || a == modelWaitNoticeID("R", "EXEC-1/TASK-1#2", 2) {
		t.Fatalf("notice identity %q %q", a, b)
	}
}
