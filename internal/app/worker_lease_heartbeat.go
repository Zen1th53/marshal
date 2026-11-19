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
const workerLeaseRenewInterval = 30 * time.Second

// keepWorkerLeaseAlive renews the task's mutation lease from the worker
// process's own heartbeats for as long as that process is alive. A slow but
// working attempt is no longer killed when the fixed lease TTL runs out. The
// renewal is bounded by execution.WorkerLeaseMaxLifetime: once it is refused
// the returned context is cancelled with the lease error, so a hung worker
// still ends. done reports that lease error, if any, after the run.
func (r *Runtime) keepWorkerLeaseAlive(ctx context.Context, task execution.TaskExecution, request *adapter.Request) (context.Context, func() error) {
	if !task.Mutates || task.LeaseID == "" {
		return ctx, func() error { return nil }
	}
	service := r.Execution()
	if service.available() != nil {
		return ctx, func() error { return nil }
	}
	engine := service.Engine()
	return workerLeaseHeartbeat(ctx, request, workerLeaseRenewInterval, func() error { return engine.RenewWorkerMutationLease(task) })
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
