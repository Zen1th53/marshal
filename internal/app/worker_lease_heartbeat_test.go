//go:build unix

package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/worker"
)

// A slow but live worker process keeps its mutation lease: the worker
// manager's heartbeats renew it, so the attempt outlives the lease TTL (the
// VM run lost a Codex attempt doing real work when its 5 minute lease ran
// out). The renewal is bounded: past the hard limit the worker is stopped
// and the attempt fails as a lease expiry.
func TestLiveWorkerHeartbeatsRenewItsLease(t *testing.T) {
	const ttl = 300 * time.Millisecond
	lm := execution.NewLeaseManager()
	lease, err := lm.AcquireLease(execution.AcquireLeaseRequest{RunID: "r", TaskID: "t", AgentID: "codex", TTL: ttl, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	renew := func(limit time.Duration) func() error {
		return func() error { return lm.RenewLeaseBounded(lease.LeaseID, "codex", ttl, limit, time.Now().UTC()) }
	}
	request := adapter.Request{}
	ctx, done := workerLeaseHeartbeat(t.Context(), &request, 60*time.Millisecond, renew(time.Hour))
	if request.Heartbeat == nil || request.HeartbeatInterval != 60*time.Millisecond {
		t.Fatalf("worker heartbeat not wired: %v", request.HeartbeatInterval)
	}
	// The process lives for three lease TTLs.
	result, err := worker.New(0, time.Second, 1<<16).Run(ctx, adapter.Command{Path: "/bin/sleep", Args: []string{"0.9"}, Heartbeat: request.Heartbeat, HeartbeatInterval: request.HeartbeatInterval})
	if err != nil || result.ExitCode != 0 || result.Cancelled {
		t.Fatalf("live worker stopped: %v %+v", err, result)
	}
	if err := withWorkerLeaseOutcome(nil, done()); err != nil {
		t.Fatalf("live worker's lease was refused: %v", err)
	}
	if err := lm.VerifyOwner("t", "codex", lease.LeaseID, time.Now().UTC()); err != nil {
		t.Fatalf("a live worker's lease expired: %v", err)
	}

	// A worker that never ends is stopped at the hard bound.
	request = adapter.Request{}
	ctx, done = workerLeaseHeartbeat(t.Context(), &request, 60*time.Millisecond, renew(time.Since(lease.AcquiredAt)+200*time.Millisecond))
	started := time.Now()
	result, _ = worker.New(0, time.Second, 1<<16).Run(ctx, adapter.Command{Path: "/bin/sleep", Args: []string{"30"}, Heartbeat: request.Heartbeat, HeartbeatInterval: request.HeartbeatInterval})
	if time.Since(started) > 10*time.Second {
		t.Fatal("a hung worker outlived its lease bound")
	}
	runErr := withWorkerLeaseOutcome(errors.New("worker cancelled"), done())
	if !errors.Is(runErr, execution.ErrLeaseExpired) || !errors.Is(context.Cause(ctx), execution.ErrLeaseExpired) {
		t.Fatalf("bounded lease did not end the attempt as a lease expiry: %v (cause %v, result %+v)", runErr, context.Cause(ctx), result)
	}
	if kind, _ := classifyInfraFailure("worker failed: run failed: one or more tasks failed: " + runErr.Error()); kind != workerLeaseExpired {
		t.Fatalf("lease expiry classified as %q", kind)
	}
}

// The VM run's lease expiry and MCP transport failures were shown as
// "crash", and two of them cooled the provider down, so the run stopped as
// waiting on a quota the provider still had.
func TestLeaseAndToolFailuresAreNamedAndNeverCoolProviders(t *testing.T) {
	lease := "worker failed: run failed: one or more tasks failed: execution lease expired: lease lease-browser-fixes-codex-1795102349103936264 expired at 2026-11-18 10:00:00 +0000 UTC"
	kind, _ := classifyInfraFailure(lease)
	if kind != workerLeaseExpired {
		t.Fatalf("lease expiry classified as %q", kind)
	}
	if got := infraFailurePhrase("Codex", kind); got != "the task's time ran out on Codex" {
		t.Fatalf("lease phrase %q", got)
	}
	stdio := strings.Repeat("codex banner line\n", 60) + "2026-11-18T10:00:00Z ERROR rmcp::transport::worker: worker quit with fatal: Transport channel closed, when Client(Send)\n" + strings.Repeat("tail line\n", 60)
	message := workerFailureMessage("Codex execution did not complete successfully", []byte(stdio), nil)
	kind, reason := classifyInfraFailure("worker failed: " + message)
	if kind != marshalToolsLost || !strings.Contains(reason, "Transport channel closed") {
		t.Fatalf("stdio MCP failure classified as %q: %s", kind, reason)
	}
	if got := infraFailurePhrase("Codex", kind); got != "Codex lost its connection to Marshal's tools" {
		t.Fatalf("tools phrase %q", got)
	}
	// Marshal's server is stdio, so an HTTP MCP client failure is another
	// (online) tool server: still carried, named a network problem.
	http := strings.Repeat("codex banner line\n", 60) + "2026-11-18T10:00:00Z ERROR codex_models_manager::manager: failed to refresh available models: request timed out\n2026-11-18T10:00:01Z ERROR rmcp::transport::worker: worker quit with fatal: Transport channel closed, when Client(HttpRequest(Timeout))\n" + strings.Repeat("tail line\n", 60)
	message = workerFailureMessage("Codex execution did not complete successfully", []byte(http), nil)
	kind, reason = classifyInfraFailure("worker failed: " + message)
	if kind != "transport or network" || !strings.Contains(reason, "Transport channel closed") {
		t.Fatalf("HTTP MCP failure classified as %q: %s", kind, reason)
	}
	for _, k := range []string{workerLeaseExpired, marshalToolsLost, "worker process failure"} {
		if infraKindCoolsProvider(k) {
			t.Errorf("%q cools the provider", k)
		}
	}
	if !infraKindCoolsProvider("provider quota or rate limit") || !infraKindCoolsProvider("transport or network") {
		t.Fatal("provider-side failures no longer cool the provider")
	}
}
