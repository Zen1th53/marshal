package app

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
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

// workerBeatRegistry holds the governed worker attempts in flight. Governed
// workers run as headless processes with no tmux window, so this is the
// only place the Board can learn that one is alive.
type workerBeatRegistry struct {
	mu    sync.Mutex
	next  uint64
	beats map[uint64]*WorkerBeat
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
	now := time.Now().UTC()
	g.mu.Lock()
	if g.beats == nil {
		g.beats = map[uint64]*WorkerBeat{}
	}
	g.next++
	id := g.next
	g.beats[id] = &WorkerBeat{RunID: runID, TaskID: taskID, Worker: worker, Started: now, Beat: now}
	g.mu.Unlock()
	var once sync.Once
	return context.WithValue(ctx, workerBeatKey{}, workerBeatHandle{g, id}), func() {
		once.Do(func() {
			g.mu.Lock()
			delete(g.beats, id)
			g.mu.Unlock()
		})
	}
}

// noteWorkerBeat renews the Beat of the attempt ctx carries, if any.
func noteWorkerBeat(ctx context.Context) {
	h, ok := ctx.Value(workerBeatKey{}).(workerBeatHandle)
	if !ok || h.registry == nil {
		return
	}
	now := time.Now().UTC()
	h.registry.mu.Lock()
	if b := h.registry.beats[h.id]; b != nil {
		b.Beat = now
	}
	h.registry.mu.Unlock()
}

// beatWorkerRequest makes the worker process's heartbeats renew the attempt
// ctx carries. It sets a heartbeat interval when the request has none, so a
// nonmutating attempt, which renews no lease, still reports it is alive.
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
			out = append(out, *b)
		}
	}
	g.mu.Unlock()
	slices.SortFunc(out, func(x, y WorkerBeat) int {
		return cmp.Or(strings.Compare(x.TaskID, y.TaskID), x.Started.Compare(y.Started))
	})
	return out
}
