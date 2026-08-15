package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

type a06Evaluator struct {
	calls    int
	decision policy.Decision
	err      error
}

func (e *a06Evaluator) Evaluate(context.Context, policy.EvaluationRequest) (policy.Decision, error) {
	e.calls++
	return e.decision, e.err
}

func a06Suite(t *testing.T, p policy.Policy, digest policy.PolicyDigest) policytest.Suite {
	t.Helper()
	suite, err := policytest.NewSuite(policytest.Suite{ID: "suite-1", Cases: []policytest.Case{{
		ID: "case-1", Name: "default deny",
		Given:  policytest.Given{Policy: p, Binding: policy.PolicyBinding{Version: p.Version, Digest: digest, Generation: 4}},
		When:   policy.EvaluationRequest{SubjectID: "subject-1", Action: "read", Resource: "repo"},
		Expect: policytest.Expectation{Decision: policy.EffectDeny},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return suite
}

func TestPolicyTestRunnerExpectedDenyPassesAndEmitsCaseEvent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite := a06Suite(t, p, digest)
	run := policytest.TestRun{ID: "run-a06-pass", PolicyID: p.ID, Binding: suite.Cases[0].Given.Binding, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	evaluator := &a06Evaluator{decision: policy.Decision{Effect: policy.EffectDeny, PolicyDigest: digest, Binding: policy.PolicyBinding{Version: 1, Digest: digest}}}
	result, err := st.RunPolicyTest(ctx, policytest.RunRequest{RunID: run.ID, Suite: suite, TestFileDigest: digest, SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1", Evaluator: evaluator, Authorizer: policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != policytest.StatusPass || len(result.Cases) != 1 || result.Cases[0].Result.Status != policytest.StatusPass || evaluator.calls != 1 {
		t.Fatalf("result = %#v, calls=%d", result, evaluator.calls)
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil || got.State != policytest.StatePassed {
		t.Fatalf("run = %#v, err=%v", got, err)
	}
	events, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	caseEvents := 0
	for _, event := range events {
		if event.Type == string(policytest.EventCasePassed) {
			caseEvents++
		}
	}
	if caseEvents != 1 {
		t.Fatalf("case passed events = %d, want 1", caseEvents)
	}
	var integrity string
	if err := st.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q, err=%v", integrity, err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM pragma_foreign_key_check"); got != 0 {
		t.Fatalf("foreign key violations = %d", got)
	}
}

func TestPolicyTestRunnerRejectsDigestAndCaseSetMismatchBeforeEvaluation(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite := a06Suite(t, p, digest)
	run := policytest.TestRun{ID: "run-a06-integrity", PolicyID: p.ID, Binding: suite.Cases[0].Given.Binding, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	evaluator := &a06Evaluator{decision: policy.Decision{Effect: policy.EffectDeny, PolicyDigest: digest, Binding: policy.PolicyBinding{Version: 1, Digest: digest}}}
	base := policytest.RunRequest{RunID: run.ID, Suite: suite, TestFileDigest: digest, SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1", Evaluator: evaluator, Authorizer: policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})}
	wrongDigest := base
	wrongDigest.TestFileDigest = policy.PolicyDigest("sha256:" + strings.Repeat("1", 64))
	if _, err := st.RunPolicyTest(ctx, wrongDigest); !errors.Is(err, policytest.ErrCaseInvalid) {
		t.Fatalf("wrong digest error = %v", err)
	}
	if evaluator.calls != 0 {
		t.Fatalf("evaluator calls after digest mismatch = %d", evaluator.calls)
	}
	missing := base
	missing.Suite.Cases = nil
	if _, err := st.RunPolicyTest(ctx, missing); !errors.Is(err, policytest.ErrCaseInvalid) {
		t.Fatalf("case-set mismatch error = %v", err)
	}
	if evaluator.calls != 0 {
		t.Fatalf("evaluator calls after case mismatch = %d", evaluator.calls)
	}
}

func TestPolicyTestRunnerCaseEvidenceSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite := a06Suite(t, p, digest)
	run := policytest.TestRun{ID: "run-a06-reopen", PolicyID: p.ID, Binding: suite.Cases[0].Given.Binding, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	allow := policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})
	if _, err := st.RunPolicyTest(ctx, policytest.RunRequest{RunID: run.ID, Suite: suite, TestFileDigest: digest, SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1", Evaluator: &a06Evaluator{decision: policy.Decision{Effect: policy.EffectDeny, PolicyDigest: digest, Binding: policy.PolicyBinding{Version: 1, Digest: digest}}}, Authorizer: allow}); err != nil {
		t.Fatal(err)
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
	events, err := reopened.ListEvents(ctx)
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
		t.Fatalf("reopened case events = %d, want 1", passed)
	}
	got, err := reopened.GetPolicyTestRun(ctx, run.ID)
	if err != nil || got.State != policytest.StatePassed {
		t.Fatalf("reopened run = %#v, err=%v", got, err)
	}
}

func TestPolicyTestRunnerUnexpectedDecisionFailsAndEmitsFailedCaseEvent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite, err := policytest.NewSuite(policytest.Suite{ID: "suite-fail", Cases: []policytest.Case{{
		ID: "case-1", Name: "must allow", Given: policytest.Given{Policy: p, Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 4}},
		When: policy.EvaluationRequest{SubjectID: "subject-1", Action: "read", Resource: "repo"}, Expect: policytest.Expectation{Decision: policy.EffectAllow},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	run := policytest.TestRun{ID: "run-a06-fail", PolicyID: p.ID, Binding: suite.Cases[0].Given.Binding, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	evaluator := &a06Evaluator{decision: policy.Decision{Effect: policy.EffectDeny, PolicyDigest: digest, Binding: policy.PolicyBinding{Version: 1, Digest: digest}}}
	result, err := st.RunPolicyTest(ctx, policytest.RunRequest{RunID: run.ID, Suite: suite, TestFileDigest: digest, SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1", Evaluator: evaluator, Authorizer: policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
		return a04Allowed(req), nil
	})})
	if err != nil || result.Status != policytest.StatusFail || result.Cases[0].Result.Status != policytest.StatusFail {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil || got.State != policytest.StateFailed {
		t.Fatalf("run=%#v err=%v", got, err)
	}
	events, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, event := range events {
		if event.Type == string(policytest.EventCaseFailed) {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("case failed events=%d, want 1", failed)
	}
}
