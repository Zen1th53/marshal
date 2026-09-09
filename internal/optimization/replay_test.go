package optimization

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/learning"
)

type recordingReplayRunner struct {
	called  bool
	request ReplayRequest
	result  ReplayObservation
}

func (r *recordingReplayRunner) Replay(_ context.Context, request ReplayRequest) (ReplayObservation, error) {
	r.called = true
	r.request = request
	return r.result, nil
}

func TestExecuteReplayInvokesAlternateOnlyInOfflineBoundedSandbox(t *testing.T) {
	runner := &recordingReplayRunner{result: ReplayObservation{
		Outcome: learning.OutcomeVerifiedComplete, VerifierResult: StatusPass,
	}}
	factual := cfFactual(learning.OutcomeFailed, StatusFail)
	got, err := ExecuteReplay(context.Background(), runner, factual, cfRoute("claude"), cfSandbox(), "integration-test", "cluster-a", cfGovernance(), cfNow())
	if err != nil {
		t.Fatalf("ExecuteReplay: %v", err)
	}
	if !runner.called {
		t.Fatal("alternate route was not actually executed")
	}
	if runner.request.Sandbox.NetworkEnabled || runner.request.Sandbox.ProductionCredentials {
		t.Fatalf("unsafe replay request: %+v", runner.request.Sandbox)
	}
	if runner.request.TaskID != factual.TaskID || runner.request.TreeDigest != factual.TreeDigest {
		t.Fatalf("replay lost factual binding: %+v", runner.request)
	}
	if got.Method != MethodReplay || got.AlternateVerifier != StatusPass {
		t.Fatalf("unexpected replay record: %+v", got)
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("replay result is not tamper-evident: %v", err)
	}
}

func TestExecuteReplayRefusesUnsafeWorkBeforeCallingRunner(t *testing.T) {
	runner := &recordingReplayRunner{}
	factual := cfFactual(learning.OutcomeFailed, StatusFail)
	factual.SideEffects = []SideEffect{{Kind: "deploy", Destructive: true}}
	_, err := ExecuteReplay(context.Background(), runner, factual, cfRoute("claude"), cfSandbox(), "test", "c", cfGovernance(), cfNow())
	if !errors.Is(err, ErrUnsafeCounterfactual) {
		t.Fatalf("ExecuteReplay error = %v, want unsafe counterfactual", err)
	}
	if runner.called {
		t.Fatal("unsafe work reached replay runner")
	}
}

func TestExecuteReplayRefusesNetworkEnabledSandboxBeforeCallingRunner(t *testing.T) {
	runner := &recordingReplayRunner{}
	sandbox := cfSandbox()
	sandbox.NetworkEnabled = true
	_, err := ExecuteReplay(context.Background(), runner, cfFactual(learning.OutcomeFailed, StatusFail), cfRoute("claude"), sandbox, "test", "c", cfGovernance(), cfNow())
	if !errors.Is(err, ErrUnsafeCounterfactual) {
		t.Fatalf("ExecuteReplay error = %v, want unsafe counterfactual", err)
	}
	if runner.called {
		t.Fatal("networked work reached replay runner")
	}
}
