package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// WorkerBeat is one governed worker attempt this process is running for a
// Marshal task, as its own heartbeats report it. Started is when the
// attempt was dispatched; Beat is the latest sign of life: the worker
// process's last heartbeat, or Started before its first one.
type WorkerBeat struct {
	RunID, TaskID, Worker string
	Started, Beat         time.Time
}

// workerBeatInterval is how often a running governed worker reports it is
// alive. It matches the lease renewal cadence the same heartbeat drives.
const workerBeatInterval = workerLeaseRenewInterval

// WorkerSignalGrace is how long a worker process may go without a
// heartbeat before Marshal stops vouching for it. Silence past it makes the
// process unverifiable, never exited: only the attempt's own end says that.
const WorkerSignalGrace = 2 * time.Minute

// ModelWaitNoticeAfter is how long a worker may wait continuously for its
// model before the Marshal and the Boards are told, once per wait.
const ModelWaitNoticeAfter = 2 * time.Minute

// Process and turn values of a worker's liveness.
const (
	ProcessLive         = "live"
	ProcessExited       = "exited"
	ProcessUnverifiable = "unverifiable"

	TurnWorking = "working"
	TurnAtRest  = "at_rest"
	TurnGone    = "gone"
	TurnUnknown = "unknown"
)

// RunNoticeModelWait is the run notice state of a worker waiting long on
// its model.
const RunNoticeModelWait = "model_wait"

// WorkerLiveness is what Marshal can verify of one task's worker attempt.
// Process comes from the attempt's own heartbeats and end, Turn from the
// provider's stream events; neither is inferred from silence. LastAction is
// one of a fixed set of words, never provider output.
type WorkerLiveness struct {
	TaskID         string     `json:"task"`
	AttemptID      string     `json:"attemptId,omitempty"`
	Process        string     `json:"process"`
	Turn           string     `json:"turn"`
	LastSignalAt   *time.Time `json:"lastSignalAt,omitempty"`
	LastAction     string     `json:"lastAction,omitempty"`
	ModelWaitSince *time.Time `json:"modelWaitSince,omitempty"`
	Stale          bool       `json:"stale"`
	Note           string     `json:"note,omitempty"`
}

// workerAttempt is the registry's record of one attempt.
type workerAttempt struct {
	WorkerBeat
	attemptID  string
	beaten     bool
	turn       string
	lastAction string
	lastSignal time.Time
	waitSince  time.Time
	waitGen    int
	signalled  int // the wait generation already announced
	tools      int // tool calls in flight
	exitedAt   time.Time
}

// workerBeatRegistry holds the governed worker attempts in flight. Governed
// workers run as headless processes with no tmux window, so this is the
// only place the Board can learn that one is alive. Ended attempts are kept
// briefly, without their context, so their exit can be shown.
type workerBeatRegistry struct {
	mu    sync.Mutex
	next  uint64
	beats map[uint64]*workerAttempt
	ended []*workerAttempt
	// now is the liveness clock; nil is the wall clock (a test moves it).
	now func() time.Time
}

// endedAttemptsKept bounds the ended attempts kept, endedAttemptTTL how long.
const (
	endedAttemptsKept = 32
	endedAttemptTTL   = 10 * time.Minute
)

func (g *workerBeatRegistry) clock() time.Time {
	if g.now != nil {
		return g.now().UTC()
	}
	return time.Now().UTC()
}

type workerBeatKey struct{}

type workerBeatHandle struct {
	registry *workerBeatRegistry
	id       uint64
}

// trackWorker records a governed worker attempt for runID's plan task until
// the returned function is called. The returned context carries the attempt,
// so the worker process's heartbeats renew its Beat (beatWorkerRequest).
func (r *Runtime) trackWorker(ctx context.Context, runID, taskID, worker string) (context.Context, func()) {
	if r == nil || runID == "" || taskID == "" {
		return ctx, func() {}
	}
	g := &r.workerBeats
	attemptID, err := model.NewID("ATTEMPT-")
	if err != nil {
		attemptID = ""
	}
	g.mu.Lock()
	now := g.clock()
	if g.beats == nil {
		g.beats = map[uint64]*workerAttempt{}
	}
	g.next++
	id := g.next
	g.beats[id] = &workerAttempt{WorkerBeat: WorkerBeat{RunID: runID, TaskID: taskID, Worker: worker, Started: now, Beat: now}, attemptID: attemptID, lastAction: "dispatched", lastSignal: now}
	g.mu.Unlock()
	var once sync.Once
	return context.WithValue(ctx, workerBeatKey{}, workerBeatHandle{g, id}), func() {
		once.Do(func() {
			g.mu.Lock()
			if a := g.beats[id]; a != nil {
				delete(g.beats, id)
				a.exitedAt, a.waitSince, a.turn, a.lastAction = g.clock(), time.Time{}, TurnGone, "exited"
				g.ended = append(g.ended, a)
			}
			g.pruneLocked()
			g.mu.Unlock()
		})
	}
}

// pruneLocked drops ended attempts past their keep time or count.
func (g *workerBeatRegistry) pruneLocked() {
	now := g.clock()
	g.ended = slices.DeleteFunc(g.ended, func(a *workerAttempt) bool { return now.Sub(a.exitedAt) > endedAttemptTTL })
	if n := len(g.ended) - endedAttemptsKept; n > 0 {
		g.ended = slices.Delete(g.ended, 0, n)
	}
}

// attempt is the live record ctx carries, under the registry lock.
func (h workerBeatHandle) attempt() *workerAttempt {
	return h.registry.beats[h.id]
}

// noteWorkerBeat renews the Beat of the attempt ctx carries, if any. A
// context of an attempt that has ended renews nothing, whatever task it
// was for.
func noteWorkerBeat(ctx context.Context) {
	h, ok := ctx.Value(workerBeatKey{}).(workerBeatHandle)
	if !ok || h.registry == nil {
		return
	}
	h.registry.mu.Lock()
	if b := h.attempt(); b != nil {
		now := h.registry.clock()
		b.Beat, b.beaten, b.lastSignal = now, true, now
	}
	h.registry.mu.Unlock()
}

// bindWorkerAttempt names the attempt ctx carries by its execution task and
// attempt count, which stay the same when Marshal restarts.
func bindWorkerAttempt(ctx context.Context, task execution.TaskExecution) {
	h, ok := ctx.Value(workerBeatKey{}).(workerBeatHandle)
	if !ok || h.registry == nil || task.TaskID == "" {
		return
	}
	h.registry.mu.Lock()
	if b := h.attempt(); b != nil {
		b.attemptID = fmt.Sprintf("%s/%s#%d", task.RunID, task.TaskID, task.Attempts)
	}
	h.registry.mu.Unlock()
}

// noteWorkerSignal records a provider stream signal of the attempt ctx
// carries. The request start, and the last tool result handed back to the
// model, start a model wait; any other output from the model ends it.
func noteWorkerSignal(ctx context.Context, signal adapter.StreamSignal) {
	h, ok := ctx.Value(workerBeatKey{}).(workerBeatHandle)
	if !ok || h.registry == nil {
		return
	}
	h.registry.mu.Lock()
	defer h.registry.mu.Unlock()
	b := h.attempt()
	if b == nil {
		return
	}
	now := h.registry.clock()
	b.lastSignal = now
	switch signal {
	case adapter.SignalRequestStart, adapter.SignalToolEnd:
		b.turn = TurnWorking
		if signal == adapter.SignalToolEnd && b.tools > 0 {
			b.tools--
		}
		if b.waitSince.IsZero() && b.tools == 0 {
			b.waitGen++
			b.waitSince = now
		}
		b.lastAction = "model request"
		if signal == adapter.SignalToolEnd {
			b.lastAction = "tool done"
		}
	case adapter.SignalToolStart:
		b.tools++
		b.turn, b.waitSince, b.lastAction = TurnWorking, time.Time{}, "tool call"
	case adapter.SignalDone:
		b.tools = 0
		b.turn, b.waitSince, b.lastAction = TurnAtRest, time.Time{}, "turn done"
	default:
		if b.turn == "" {
			return // output before any instrumented start says nothing
		}
		b.turn, b.waitSince, b.lastAction = TurnWorking, time.Time{}, "output"
	}
}

// beatWorkerRequest makes the worker process's heartbeats renew the attempt
// ctx carries. It sets a heartbeat interval when the request has none, so a
// nonmutating attempt, which renews no lease, still reports it is alive.
// The provider's stream signals follow the attempt's model turn.
func beatWorkerRequest(ctx context.Context, request *adapter.Request) {
	if _, ok := ctx.Value(workerBeatKey{}).(workerBeatHandle); !ok {
		return
	}
	inner := request.Heartbeat
	request.Heartbeat = func() {
		noteWorkerBeat(ctx)
		if inner != nil {
			inner()
		}
	}
	progress := request.Progress
	request.Progress = func(signal adapter.StreamSignal) {
		noteWorkerSignal(ctx, signal)
		if progress != nil {
			progress(signal)
		}
	}
	if request.HeartbeatInterval <= 0 || request.HeartbeatInterval > workerBeatInterval {
		request.HeartbeatInterval = workerBeatInterval
	}
}

// WorkerBeats lists the governed worker attempts in flight for runID, in
// task order.
func (r *Runtime) WorkerBeats(runID string) []WorkerBeat {
	if r == nil {
		return nil
	}
	g := &r.workerBeats
	g.mu.Lock()
	out := make([]WorkerBeat, 0, len(g.beats))
	for _, b := range g.beats {
		if b.RunID == runID {
			out = append(out, b.WorkerBeat)
		}
	}
	g.mu.Unlock()
	slices.SortFunc(out, func(x, y WorkerBeat) int {
		return cmp.Or(strings.Compare(x.TaskID, y.TaskID), x.Started.Compare(y.Started))
	})
	return out
}

// liveness projects one attempt at now.
func (a *workerAttempt) liveness(now time.Time) WorkerLiveness {
	l := WorkerLiveness{TaskID: a.TaskID, AttemptID: a.attemptID, LastAction: a.lastAction, Turn: TurnUnknown}
	if !a.lastSignal.IsZero() {
		at := a.lastSignal
		l.LastSignalAt = &at
	}
	switch {
	case !a.exitedAt.IsZero():
		l.Process, l.Turn = ProcessExited, TurnGone
		return l
	case !a.beaten:
		l.Process, l.Stale = ProcessUnverifiable, now.Sub(a.Started) > WorkerSignalGrace
		l.Note = "no heartbeat from the worker process yet; Marshal cannot verify it is running"
		return l
	case now.Sub(a.Beat) > WorkerSignalGrace:
		l.Process, l.Stale = ProcessUnverifiable, true
		l.Note = "no heartbeat for " + now.Sub(a.Beat).Truncate(time.Second).String() + "; the worker may still be running, Marshal cannot verify it"
		return l
	}
	l.Process = ProcessLive
	if a.turn != "" {
		l.Turn = a.turn
	}
	if !a.waitSince.IsZero() {
		since := a.waitSince
		l.ModelWaitSince = &since
		if wait := now.Sub(since); wait >= ModelWaitNoticeAfter {
			l.Note = "waiting on its model for " + wait.Truncate(time.Second).String()
		}
	}
	return l
}

// WorkerLiveness lists what Marshal can verify of each worker attempt of
// runID it ran, in task order: those in flight, then those that ended.
func (r *Runtime) WorkerLiveness(runID string) []WorkerLiveness {
	if r == nil {
		return nil
	}
	g := &r.workerBeats
	g.mu.Lock()
	g.pruneLocked()
	now := g.clock()
	var attempts []*workerAttempt
	for _, a := range g.beats {
		if a.RunID == runID {
			attempts = append(attempts, a)
		}
	}
	for _, a := range g.ended {
		if a.RunID == runID {
			attempts = append(attempts, a)
		}
	}
	slices.SortStableFunc(attempts, func(x, y *workerAttempt) int {
		return cmp.Or(strings.Compare(x.TaskID, y.TaskID), x.Started.Compare(y.Started))
	})
	out := make([]WorkerLiveness, 0, len(attempts))
	for _, a := range attempts {
		out = append(out, a.liveness(now))
	}
	g.mu.Unlock()
	return out
}

// RunLiveness is the liveness of every task of run that a worker holds or
// held, one row per task: its latest attempt. A dispatched task whose
// attempt this process does not hold (Marshal restarted, or the process
// evidence was lost) is unverifiable, never exited.
func (r *Runtime) RunLiveness(runID string, run marshal.Run) []WorkerLiveness {
	latest := map[string]WorkerLiveness{}
	live := map[string]bool{}
	for _, l := range r.WorkerLiveness(runID) {
		// An attempt in flight wins over one that ended.
		if l.Process != ProcessExited || !live[l.TaskID] {
			latest[l.TaskID] = l
			live[l.TaskID] = live[l.TaskID] || l.Process != ProcessExited
		}
	}
	var out []WorkerLiveness
	for _, t := range run.Tasks {
		if l, ok := latest[t.PlanTaskID]; ok {
			out = append(out, l)
			continue
		}
		if t.State == marshal.Dispatched {
			out = append(out, WorkerLiveness{TaskID: t.PlanTaskID, Process: ProcessUnverifiable, Turn: TurnUnknown, Stale: true,
				Note: "Marshal holds no evidence of this task's worker process (it may have restarted); it cannot verify the worker"})
		}
	}
	return out
}

// modelWaitNoticeID is the stable notice identity of one wait of one
// attempt: the same after a restart for the same attempt and wait.
func modelWaitNoticeID(runID, attemptID string, gen int) string {
	sum := sha256.Sum256([]byte(attemptID))
	return fmt.Sprintf("run:%s:model-wait:%s:%d", runID, hex.EncodeToString(sum[:8]), gen)
}

// SignalModelWaits records one run notice for each worker wait on its
// model that has lasted ModelWaitNoticeAfter, once per attempt and wait.
// It returns the runs with a wait announced now.
func (r *Runtime) SignalModelWaits(ctx context.Context) []string {
	if r == nil {
		return nil
	}
	type due struct {
		a      *workerAttempt
		gen    int
		notice RunNotice
	}
	var pending []due
	g := &r.workerBeats
	g.mu.Lock()
	now := g.clock()
	for _, a := range g.beats {
		if a.waitSince.IsZero() || a.signalled == a.waitGen || now.Sub(a.waitSince) < ModelWaitNoticeAfter || !a.beaten || now.Sub(a.Beat) > WorkerSignalGrace {
			continue
		}
		wait := now.Sub(a.waitSince).Truncate(time.Minute)
		pending = append(pending, due{a: a, gen: a.waitGen, notice: RunNotice{
			EventID: modelWaitNoticeID(a.RunID, a.attemptID, a.waitGen), RunID: a.RunID, TaskID: a.TaskID,
			State: RunNoticeModelWait, Code: RunNoticeModelWait, At: now,
			Summary: boundedRedacted(fmt.Sprintf("The worker on task %s has waited %s for its model; its process is still running.", a.TaskID, wait), 500),
			Next:    "Keep waiting, or /marshal stop and then /marshal resume to start the task again.",
		}})
	}
	g.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	service := r.Marshal()
	var runs []string
	for _, d := range pending {
		if service.recordRunNotice(ctx, d.notice) != nil {
			continue // tried again at the next look
		}
		g.mu.Lock()
		if d.a.signalled < d.gen {
			d.a.signalled = d.gen
		}
		g.mu.Unlock()
		if !slices.Contains(runs, d.notice.RunID) {
			runs = append(runs, d.notice.RunID)
		}
	}
	return runs
}

// TrackWorkerForTest starts a worker attempt of runID's task as a governed
// dispatch would, judged by the clock now, for tests outside this package.
// beat and signal act as the worker process's heartbeat and stream do.
func (r *Runtime) TrackWorkerForTest(runID, taskID, worker string, now func() time.Time) (beat func(), signal func(adapter.StreamSignal), end func()) {
	r.workerBeats.mu.Lock()
	r.workerBeats.now = now
	r.workerBeats.mu.Unlock()
	ctx, end := r.trackWorker(context.Background(), runID, taskID, worker)
	bindWorkerAttempt(ctx, execution.TaskExecution{RunID: runID, TaskID: taskID, Attempts: 1})
	return func() { noteWorkerBeat(ctx) }, func(s adapter.StreamSignal) { noteWorkerSignal(ctx, s) }, end
}
