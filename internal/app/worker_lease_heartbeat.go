package app

import (
	"context"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
)

// workerLeaseRenewInterval is how often a live worker's heartbeats renew its
// task's mutation lease. It is well inside execution.WorkerLeaseTTL, so a
// slow host that misses a few heartbeats still keeps the lease.
const workerLeaseRenewInterval = execution.WorkerLeaseTTL / 10

// workerLeaseRenewEvery is the renewal interval for an engine's lease TTL.
func workerLeaseRenewEvery(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return workerLeaseRenewInterval
	}
	return ttl / 10
}

type workerLeaseKeeperKey struct{}

// workerLeaseKeeper keeps one worker attempt's mutation lease from the moment
// the harness starts the attempt. The engine takes the lease before it calls
// the harness, and the harness then prepares the worker (provider probe,
// transport review, context admission, the knowledge index of a fresh lane
// worktree) before any worker process exists. On a slow host that took
// longer than the lease TTL, so the lease ran out before the process could
// renew it. While the attempt prepares, a timer renews the lease; once the
// worker process starts, renewal is handed over to the process's own
// heartbeats, so a worker whose process has died stops renewing. Every
// renewal is bounded by execution.WorkerLeaseMaxLifetime.
type workerLeaseKeeper struct {
	renew    func() error
	interval time.Duration
	cancel   context.CancelCauseFunc

	stopPrep chan struct{}
	prepDone chan struct{}
	handOnce sync.Once

	mu  sync.Mutex
	err error
}

// holdWorkerLease starts keeping task's mutation lease for this attempt. The
// returned context is cancelled with the lease error once a renewal is
// refused. Nonmutating tasks hold no lease; the keeper is then nil and every
// method is a no-op.
func (r *Runtime) holdWorkerLease(ctx context.Context, task execution.TaskExecution) (context.Context, *workerLeaseKeeper) {
	if !task.Mutates || task.LeaseID == "" {
		return ctx, nil
	}
	service := r.Execution()
	if service.available() != nil {
		return ctx, nil
	}
	engine := service.Engine()
	return startWorkerLeaseKeeper(ctx, workerLeaseRenewEvery(engine.WorkerLeaseTTL()), func() error { return engine.RenewWorkerMutationLease(task) })
}

func startWorkerLeaseKeeper(ctx context.Context, interval time.Duration, renew func() error) (context.Context, *workerLeaseKeeper) {
	keeperCtx, cancel := context.WithCancelCause(ctx)
	k := &workerLeaseKeeper{renew: renew, interval: interval, cancel: cancel, stopPrep: make(chan struct{}), prepDone: make(chan struct{})}
	keeperCtx = context.WithValue(keeperCtx, workerLeaseKeeperKey{}, k)
	// The attempt starts with a full lease, however long the engine took
	// between acquiring it and calling the harness.
	_ = k.renewNow()
	go func() {
		defer close(k.prepDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-k.stopPrep:
				return
			case <-keeperCtx.Done():
				return
			case <-ticker.C:
				if k.renewNow() != nil {
					return
				}
			}
		}
	}()
	return keeperCtx, k
}

// renewNow renews the lease once. The first refusal cancels the attempt
// with its error and is returned by every later call.
func (k *workerLeaseKeeper) renewNow() error {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return k.err
	}
	if err := k.renew(); err != nil {
		k.err = err
		k.cancel(err)
	}
	return k.err
}

// handOver stops the preparation timer. From here on only the worker
// process's heartbeats renew the lease.
func (k *workerLeaseKeeper) handOver() {
	if k == nil {
		return
	}
	k.handOnce.Do(func() {
		close(k.stopPrep)
		<-k.prepDone
	})
}

// Err reports the lease error that ended the attempt, if any.
func (k *workerLeaseKeeper) Err() error {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.err
}

// outcome ends keeping the lease when the attempt returns. A lease that ran
// out is the attempt's failure, so it is reported as the task's time running
// out and not as whatever the cancelled preparation or worker returned.
func (k *workerLeaseKeeper) outcome(err error) error {
	if k == nil {
		return err
	}
	k.handOver()
	leaseErr := k.Err()
	k.cancel(context.Canceled)
	return withWorkerLeaseOutcome(err, leaseErr)
}

// keepWorkerLeaseAlive renews the task's mutation lease from the worker
// process's own heartbeats for as long as that process is alive, silent or
// not: the worker manager beats on a timer while the process runs, not on
// its output. A slow but working attempt is no longer killed when the fixed
// lease TTL runs out. The renewal is bounded by
// execution.WorkerLeaseMaxLifetime: once it is refused the returned context
// is cancelled with the lease error, so a hung worker still ends. done
// reports that lease error, if any, after the run. When the attempt holds a
// workerLeaseKeeper (holdWorkerLease), renewal passes from its preparation
// timer to the process heartbeats at the first heartbeat.
func (r *Runtime) keepWorkerLeaseAlive(ctx context.Context, task execution.TaskExecution, request *adapter.Request) (context.Context, func() error) {
	// The same heartbeats tell the Board the worker is alive (worker_liveness.go).
	beatWorkerRequest(ctx, request)
	if !task.Mutates || task.LeaseID == "" {
		return ctx, func() error { return nil }
	}
	if k, ok := ctx.Value(workerLeaseKeeperKey{}).(*workerLeaseKeeper); ok && k != nil {
		// The adapter may still run short commands of its own (model
		// discovery, MCP setup) before the worker process starts, and those
		// do not beat. The preparation timer keeps the lease until the worker
		// process's first heartbeat, and stops when the run returns.
		workerCtx, done := workerLeaseHeartbeat(ctx, request, k.interval, k.renewNow)
		beat := request.Heartbeat
		request.Heartbeat = func() {
			k.handOver()
			beat()
		}
		return workerCtx, func() error {
			k.handOver()
			return withWorkerLeaseOutcome(done(), k.Err())
		}
	}
	service := r.Execution()
	if service.available() != nil {
		return ctx, func() error { return nil }
	}
	engine := service.Engine()
	return workerLeaseHeartbeat(ctx, request, workerLeaseRenewEvery(engine.WorkerLeaseTTL()), func() error { return engine.RenewWorkerMutationLease(task) })
}

// workerLeaseHeartbeat chains renew into request's heartbeat, renewing at
// most once per half interval. The first refused renewal cancels the
// returned context with its error and stops further renewals.
func workerLeaseHeartbeat(ctx context.Context, request *adapter.Request, interval time.Duration, renew func() error) (context.Context, func() error) {
	workerCtx, cancel := context.WithCancelCause(ctx)
	var mu sync.Mutex
	var leaseErr error
	last := time.Now()
	attempt := func() {
		mu.Lock()
		defer mu.Unlock()
		if leaseErr != nil || time.Since(last) < interval/2 {
			return
		}
		last = time.Now()
		if err := renew(); err != nil {
			leaseErr = err
			cancel(err)
		}
	}
	inner := request.Heartbeat
	request.Heartbeat = func() {
		attempt()
		if inner != nil {
			inner()
		}
	}
	if request.HeartbeatInterval <= 0 || request.HeartbeatInterval > interval {
		request.HeartbeatInterval = interval
	}
	return workerCtx, func() error {
		mu.Lock()
		defer mu.Unlock()
		return leaseErr
	}
}

// withWorkerLeaseOutcome makes a lease that ran out during the run the
// attempt's failure, so it is reported as the task's time running out and
// not as the cancelled worker's crash.
func withWorkerLeaseOutcome(runErr, leaseErr error) error {
	if leaseErr != nil {
		return leaseErr
	}
	return runErr
}
