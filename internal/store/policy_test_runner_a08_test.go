package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

type blockingA08Evaluator struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	policy.Decision
}

func (e *blockingA08Evaluator) Evaluate(context.Context, policy.EvaluationRequest) (policy.Decision, error) {
	e.calls.Add(1)
	e.started <- struct{}{}
	<-e.release
	return e.Decision, nil
}

func TestPolicyTestRunnerTwoStoresClaimOneExecution(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
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

	p := policy.Policy{ID: "policy-a08", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite := a06Suite(t, p, digest)
	run := policytest.TestRun{ID: "run-a08-owner", PolicyID: p.ID, Binding: suite.Cases[0].Given.Binding, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := first.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	authorized := a04Request(run)
	if _, err := first.TransitionPolicyTestRunStateAuthorized(ctx, authorized, policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})); err != nil {
		t.Fatal(err)
	}
	authorized.ExpectedState, authorized.TargetState = policytest.StateValidated, policytest.StateExecuted
	if _, err := first.TransitionPolicyTestRunStateAuthorized(ctx, authorized, policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})); err != nil {
		t.Fatal(err)
	}
	authorizer := policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})
	evaluator := &blockingA08Evaluator{
		started:  make(chan struct{}, 2),
		release:  make(chan struct{}),
		Decision: policy.Decision{Effect: policy.EffectDeny, PolicyDigest: digest, Binding: policy.PolicyBinding{Version: 1, Digest: digest}},
	}
	request := policytest.RunRequest{RunID: run.ID, Suite: suite, TestFileDigest: digest, SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1", Evaluator: evaluator, Authorizer: authorizer}
	results := make(chan error, 2)
	go func() { _, err := first.RunPolicyTest(ctx, request); results <- err }()
	go func() { _, err := second.RunPolicyTest(ctx, request); results <- err }()
	<-evaluator.started
	duplicateExecution := false
	select {
	case <-evaluator.started:
		duplicateExecution = true
	case <-time.After(500 * time.Millisecond):
	}
	close(evaluator.release)
	var errs []error
	for range 2 {
		errs = append(errs, <-results)
	}
	if got := evaluator.calls.Load(); got != 1 || duplicateExecution {
		t.Fatalf("evaluator calls = %d, duplicate execution=%t, want one durable execution owner; errors=%v", got, duplicateExecution, errs)
	}
	final, err := first.GetPolicyTestRun(ctx, run.ID)
	if err != nil || final.State != policytest.StatePassed {
		t.Fatalf("final run = %#v, err=%v", final, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	retry, err := reopened.RunPolicyTest(ctx, requestWithEvaluator(request, nil))
	if err != nil || retry.Status != policytest.StatusPass {
		t.Fatalf("retry result=%#v err=%v", retry, err)
	}
	events, err := reopened.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	casePassed := 0
	for _, event := range events {
		if event.Type == string(policytest.EventCasePassed) {
			casePassed++
		}
	}
	if casePassed != 1 {
		t.Fatalf("case passed events = %d, want one", casePassed)
	}
}

func TestPolicyTestRunnerHighContentionOneExecution(t *testing.T) {
	const contenders = 32
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	stores := make([]*Store, contenders)
	for i := range stores {
		st, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = st
		t.Cleanup(func() { _ = st.Close() })
	}
	if err := stores[0].Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := policy.Policy{ID: "policy-a08-contention", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite := a06Suite(t, p, digest)
	run := policytest.TestRun{ID: "run-a08-contention", PolicyID: p.ID, Binding: suite.Cases[0].Given.Binding, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := stores[0].PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	authorizer := policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})
	evaluator := &countingA08Evaluator{decision: policy.Decision{Effect: policy.EffectDeny, PolicyDigest: digest, Binding: policy.PolicyBinding{Version: 1, Digest: digest}}}
	request := policytest.RunRequest{RunID: run.ID, Suite: suite, TestFileDigest: digest, SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1", Evaluator: evaluator, Authorizer: authorizer}
	results := make(chan error, contenders)
	for _, st := range stores {
		go func(st *Store) {
			_, runErr := st.RunPolicyTest(ctx, request)
			results <- runErr
		}(st)
	}
	var errs []error
	for range stores {
		errs = append(errs, <-results)
	}
	final, err := stores[0].GetPolicyTestRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != policytest.StatePassed || evaluator.calls.Load() != 1 {
		t.Fatalf("final=%#v evaluator_calls=%d errors=%v, want one terminal owner", final, evaluator.calls.Load(), errs)
	}
	events, err := stores[0].ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	passed := 0
	for _, event := range events {
		if event.Type == string(policytest.EventCasePassed) {
			passed++
		}
	}
	if passed != 1 {
		t.Fatalf("case passed events=%d, want 1", passed)
	}
}

type countingA08Evaluator struct {
	calls    atomic.Int32
	decision policy.Decision
}

func (e *countingA08Evaluator) Evaluate(context.Context, policy.EvaluationRequest) (policy.Decision, error) {
	e.calls.Add(1)
	return e.decision, nil
}

func TestPolicyTestRunnerLiveClaimCannotBeStolen(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run, _ := prepareA08ExecutedRun(t, st, "run-a08-live-claim")
	first, err := st.claimPolicyTestExecution(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.claimPolicyTestExecution(ctx, run.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("live claim steal err=%v, want conflict", err)
	}
	if err := st.releasePolicyTestExecution(ctx, run.ID, first); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyTestRunnerStaleOwnerCannotFinalize(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run, suite := prepareA08ExecutedRun(t, st, "run-a08-stale-owner")
	run.State = policytest.StateExecuted
	oldOwner, err := st.claimPolicyTestExecution(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "UPDATE policy_test_runs SET execution_claimed_at = ? WHERE run_id = ?", time.Now().UTC().Add(-policyTestExecutionClaimTTL-time.Second).Format(time.RFC3339Nano), run.ID); err != nil {
		t.Fatal(err)
	}
	newOwner, err := st.claimPolicyTestExecution(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := policytest.RunRequest{RunID: run.ID, Suite: suite, TestFileDigest: run.TestFileDigest, SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1", Authorizer: policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})}
	result := policytest.RunResult{Status: policytest.StatusPass, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if _, err := st.authorizedRunTransitionWithCaseEvents(ctx, request, run, policytest.StatePassed, oldOwner, result, nil); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale owner finalize err=%v, want conflict", err)
	}
	if _, err := st.authorizedRunTransitionWithCaseEvents(ctx, request, run, policytest.StatePassed, newOwner, result, nil); err != nil {
		t.Fatalf("current owner finalize: %v", err)
	}
}

func TestPolicyTestRunnerCancellationDuringEvaluationReleasesClaim(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run, suite := prepareA08ExecutedRun(t, st, "run-a08-cancel")
	evaluator := &cancellingA08Evaluator{started: make(chan struct{})}
	request := policytest.RunRequest{RunID: run.ID, Suite: suite, TestFileDigest: run.TestFileDigest, SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1", Evaluator: evaluator, Authorizer: policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})}
	resultCh := make(chan error, 1)
	go func() {
		_, err := st.RunPolicyTest(ctx, request)
		resultCh <- err
	}()
	<-evaluator.started
	cancel()
	if err := <-resultCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation err=%v, want context canceled", err)
	}
	final, err := st.GetPolicyTestRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != policytest.StateExecuted {
		t.Fatalf("cancelled run state=%s, want executed", final.State)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM policy_test_outcomes WHERE run_id = ?", run.ID); got != 0 {
		t.Fatalf("cancelled outcomes=%d, want 0", got)
	}
}

func TestPolicyTestRunnerTerminalFailureReconstructsWithoutEvaluation(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := policy.Policy{ID: "policy-a08-terminal-fail", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite := a06Suite(t, p, digest)
	run := policytest.TestRun{ID: "run-a08-terminal-fail", PolicyID: p.ID, Binding: suite.Cases[0].Given.Binding, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	authorizer := policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})
	first, err := st.RunPolicyTest(ctx, policytest.RunRequest{RunID: run.ID, Suite: suite, TestFileDigest: digest, SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1", Evaluator: &a06Evaluator{decision: policy.Decision{Allowed: true, Effect: policy.EffectAllow, PolicyDigest: digest, Binding: policy.PolicyBinding{Version: 1, Digest: digest}}}, Authorizer: authorizer})
	if err != nil || first.Status != policytest.StatusFail {
		t.Fatalf("first result=%#v err=%v", first, err)
	}
	retry, err := st.RunPolicyTest(ctx, policytest.RunRequest{RunID: run.ID, Suite: suite, TestFileDigest: digest, SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1"})
	if err != nil || retry.Status != policytest.StatusFail || retry.Cases[0].Result.Diff == "" {
		t.Fatalf("retry result=%#v err=%v", retry, err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM policy_test_outcomes WHERE run_id = ?", run.ID); got != 1 {
		t.Fatalf("outcomes=%d, want 1", got)
	}
}

type cancellingA08Evaluator struct {
	started chan struct{}
}

func (e *cancellingA08Evaluator) Evaluate(ctx context.Context, _ policy.EvaluationRequest) (policy.Decision, error) {
	close(e.started)
	<-ctx.Done()
	return policy.Decision{}, ctx.Err()
}

func prepareA08ExecutedRun(t *testing.T, st *Store, id string) (policytest.TestRun, policytest.Suite) {
	t.Helper()
	p := policy.Policy{ID: policy.PolicyID("policy-" + id), Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite := a06Suite(t, p, digest)
	run := policytest.TestRun{ID: id, PolicyID: p.ID, Binding: suite.Cases[0].Given.Binding, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := st.PutPolicyTestRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	request := a04Request(run)
	allow := policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})
	if _, err := st.TransitionPolicyTestRunStateAuthorized(context.Background(), request, allow); err != nil {
		t.Fatal(err)
	}
	request.ExpectedState, request.TargetState = policytest.StateValidated, policytest.StateExecuted
	if _, err := st.TransitionPolicyTestRunStateAuthorized(context.Background(), request, allow); err != nil {
		t.Fatal(err)
	}
	return run, suite
}

func requestWithEvaluator(request policytest.RunRequest, evaluator policytest.Evaluator) policytest.RunRequest {
	request.Evaluator = evaluator
	return request
}
