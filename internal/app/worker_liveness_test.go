package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
)

// A governed worker attempt is listed for its Marshal run from dispatch
// until it ends, and every heartbeat of its process renews its Beat, so
// the Board can tell a live headless worker from a run with none.
func TestGovernedWorkerHeartbeatsAreListed(t *testing.T) {
	r := &Runtime{}
	ctx, untrack := r.trackWorker(t.Context(), "R-80", "browser-fixes", "codex")
	_, untrackOther := r.trackWorker(t.Context(), "R-other", "x", "claude")
	defer untrackOther()
	beats := r.WorkerBeats("R-80")
	if len(beats) != 1 || beats[0].TaskID != "browser-fixes" || beats[0].Worker != "codex" || beats[0].Started.IsZero() || !beats[0].Beat.Equal(beats[0].Started) {
		t.Fatalf("beats = %+v", beats)
	}
	// A nonmutating attempt renews no lease, yet its process still beats.
	request := adapter.Request{}
	_, done := r.keepWorkerLeaseAlive(ctx, execution.TaskExecution{TaskID: "browser-fixes"}, &request)
	if request.Heartbeat == nil || request.HeartbeatInterval != workerBeatInterval {
		t.Fatalf("worker heartbeat not wired: %v", request.HeartbeatInterval)
	}
	time.Sleep(5 * time.Millisecond)
	request.Heartbeat()
	if got := r.WorkerBeats("R-80")[0]; !got.Beat.After(got.Started) {
		t.Fatalf("heartbeat did not renew the beat: %+v", got)
	}
	if err := done(); err != nil {
		t.Fatal(err)
	}
	untrack()
	untrack()
	if beats := r.WorkerBeats("R-80"); len(beats) != 0 {
		t.Fatalf("ended attempt still listed: %+v", beats)
	}
	if beats := r.WorkerBeats("R-other"); len(beats) != 1 {
		t.Fatalf("another run's attempt = %+v", beats)
	}
}

// The lease heartbeat chained over the liveness beat keeps both: a mutating
// attempt's heartbeat renews its lease and its beat.
func TestWorkerBeatChainsWithLeaseRenewal(t *testing.T) {
	r := &Runtime{}
	ctx, untrack := r.trackWorker(context.Background(), "R", "t", "codex")
	defer untrack()
	request := adapter.Request{}
	beatWorkerRequest(ctx, &request)
	renewed := 0
	_, _ = workerLeaseHeartbeat(ctx, &request, time.Millisecond, func() error { renewed++; return nil })
	time.Sleep(5 * time.Millisecond)
	request.Heartbeat()
	if got := r.WorkerBeats("R")[0]; renewed != 1 || !got.Beat.After(got.Started) {
		t.Fatalf("renewed %d, beat %+v", renewed, got)
	}
	// A context with no attempt is left alone.
	plain := adapter.Request{}
	beatWorkerRequest(context.Background(), &plain)
	if plain.Heartbeat != nil || plain.HeartbeatInterval != 0 {
		t.Fatal("an untracked request was given a heartbeat")
	}
	var nilRuntime *Runtime
	if beats := nilRuntime.WorkerBeats("R"); beats != nil {
		t.Fatal("nil runtime listed beats")
	}
}
