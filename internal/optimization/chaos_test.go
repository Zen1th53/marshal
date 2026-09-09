package optimization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

type failingReplayRunner struct {
	called bool
	err    error
}

func (r *failingReplayRunner) Replay(context.Context, ReplayRequest) (ReplayObservation, error) {
	r.called = true
	return ReplayObservation{}, r.err
}

// TestChaosReplayFailureDoesNotFabricateEvidence models a runner crash,
// provider outage, or evaluator timeout. A failed replay must return its
// error, never materialize a PASS/UNKNOWN counterfactual that could later be
// mistaken for promotion evidence.
func TestChaosReplayFailureDoesNotFabricateEvidence(t *testing.T) {
	for _, cause := range []error{
		errors.New("runner crashed"),
		context.DeadlineExceeded,
		context.Canceled,
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			runner := &failingReplayRunner{err: cause}
			_, err := ExecuteReplay(context.Background(), runner,
				cfFactual(learning.OutcomeFailed, StatusFail), cfRoute("claude"), cfSandbox(),
				"chaos", "independent-cluster", cfGovernance(), cfNow())
			if !errors.Is(err, cause) {
				t.Fatalf("error=%v, want wrapped %v", err, cause)
			}
			if !runner.called {
				t.Fatal("runner fault was silently skipped")
			}
		})
	}
}

// TestChaosMissingGuardMetricStopsCanary models an interrupted or partially
// observed canary. Missing telemetry is a rollback condition, not a clean
// completion signal.
func TestChaosMissingGuardMetricStopsCanary(t *testing.T) {
	now := time.Now().UTC()
	canary := Canary{
		ID: "chaos-canary", CandidateID: "candidate", BaselineID: "baseline",
		Exposure: .1, EligibleTaskClasses: []string{"code"}, MaxTasks: 5,
		Deadline:         now.Add(time.Hour),
		RollbackTriggers: []Trigger{{Metric: "verification_failure_rate", Threshold: .01, HigherIsWorse: true, Reason: "verification regression"}},
		State:            CanaryRunning,
	}
	guard := EvaluateGuard(canary, map[string]float64{}, now)
	if !guard.Rollback || len(guard.Reasons) == 0 {
		t.Fatalf("missing guard telemetry allowed canary to continue: %+v", guard)
	}
	rolled, err := Rollback(canary, guard.Reasons[0], now)
	if err != nil || rolled.State != CanaryRolledBack || rolled.RollbackReason == "" {
		t.Fatalf("rollback did not preserve interruption evidence: canary=%+v err=%v", rolled, err)
	}
}
