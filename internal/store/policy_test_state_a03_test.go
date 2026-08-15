package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

func TestPolicyTestRunRejectsIllegalStateTransition(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	digest, err := (policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny}).Digest()
	if err != nil {
		t.Fatal(err)
	}
	run := policytest.TestRun{ID: "run-state", PolicyID: "policy-1", Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 1}, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionPolicyTestRunState(ctx, run.ID, policytest.StateLoaded, policytest.StatePassed); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("illegal transition error = %v, want invalid", err)
	}
}

func TestPolicyTestRunLifecycleCASPreservesBindingAndRestart(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.db"
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-lifecycle")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	initial, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil || initial.State != policytest.StateLoaded {
		t.Fatalf("initial state = %s, err=%v; want loaded", initial.State, err)
	}
	for _, edge := range [][2]policytest.RunState{{policytest.StateLoaded, policytest.StateValidated}, {policytest.StateValidated, policytest.StateExecuted}, {policytest.StateExecuted, policytest.StatePassed}} {
		if _, err := st.TransitionPolicyTestRunState(ctx, run.ID, edge[0], edge[1]); err != nil {
			t.Fatalf("transition %s -> %s: %v", edge[0], edge[1], err)
		}
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != policytest.StatePassed || got.PolicyID != run.PolicyID || got.Binding != run.Binding || got.TestFileDigest != run.TestFileDigest {
		t.Fatalf("lifecycle changed immutable binding: %#v", got)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.GetPolicyTestRun(ctx, run.ID); err != nil || got.State != policytest.StatePassed {
		t.Fatalf("reopened state = %#v, err=%v", got, err)
	}
}

func TestPolicyTestRunStaleCASAndCancellationDoNotMutate(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.db"
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-stale")
	if err := first.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := first.TransitionPolicyTestRunState(ctx, run.ID, policytest.StateLoaded, policytest.StateValidated); err != nil {
		t.Fatal(err)
	}
	if _, err := second.TransitionPolicyTestRunState(ctx, run.ID, policytest.StateLoaded, policytest.StateValidated); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale transition error = %v, want conflict", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := first.TransitionPolicyTestRunState(cancelled, run.ID, policytest.StateValidated, policytest.StateExecuted); err == nil {
		t.Fatal("cancelled transition unexpectedly succeeded")
	}
	got, err := first.GetPolicyTestRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != policytest.StateValidated {
		t.Fatalf("cancelled/stale transition changed state to %s", got.State)
	}
}

func TestPolicyTestRunRejectsCorruptDurableState(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run := a03TestRun(t, "run-corrupt-state")
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "UPDATE policy_test_runs SET state = 'unknown' WHERE run_id = ?", run.ID); err == nil {
		t.Fatal("database accepted corrupt state")
	}
}

func a03TestRun(t *testing.T, id string) policytest.TestRun {
	t.Helper()
	digest, err := (policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny}).Digest()
	if err != nil {
		t.Fatal(err)
	}
	return policytest.TestRun{ID: id, PolicyID: "policy-1", Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 4}, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
}
