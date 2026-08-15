package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

func TestPolicyTestRunnerUnknownEvaluatorErrorHasTypedFailedCaseEvent(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T49_A07_STORE_7f2a"
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := policy.Policy{ID: "policy-a07", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite := a06Suite(t, p, digest)
	run := policytest.TestRun{ID: "run-a07-error", PolicyID: p.ID, Binding: suite.Cases[0].Given.Binding, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	result, err := st.RunPolicyTest(ctx, policytest.RunRequest{
		RunID: run.ID, Suite: suite, TestFileDigest: digest,
		SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1",
		Evaluator: &a06Evaluator{err: errors.New("backend failure: " + marker)},
		Authorizer: policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
			return a04Allowed(req), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != policytest.StatusError || result.Cases[0].Result.Reason != policy.ErrorCode(policytest.CodeCaseInvalid) {
		t.Fatalf("result = %#v", result)
	}
	events, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, event := range events {
		if event.Type == string(policytest.EventCaseFailed) {
			failed++
			if event.Data["reason_code"] != string(policy.ErrorCode(policytest.CodeCaseInvalid)) {
				t.Fatalf("failure reason = %#v", event.Data["reason_code"])
			}
		}
		if strings.Contains(event.Data["reason_code"].(string), marker) {
			t.Fatal("secret marker leaked in case event")
		}
	}
	if failed != 1 {
		t.Fatalf("failed case events = %d, want 1", failed)
	}
	var durable string
	if err := st.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT group_concat(data_json) FROM audit_events), '') || COALESCE((SELECT group_concat(reason) FROM policy_test_cases), '')`).Scan(&durable); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(durable, marker) {
		t.Fatal("secret marker leaked into durable store")
	}
}

func TestPolicyTestRunnerTerminalAuthorizationFailureDoesNotPersistCaseEvents(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := policy.Policy{ID: "policy-a07-transaction", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite := a06Suite(t, p, digest)
	run := policytest.TestRun{ID: "run-a07-transaction", PolicyID: p.ID, Binding: suite.Cases[0].Given.Binding, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	_, err = st.RunPolicyTest(ctx, policytest.RunRequest{
		RunID: run.ID, Suite: suite, TestFileDigest: digest,
		SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1",
		Evaluator: &a06Evaluator{decision: policy.Decision{Effect: policy.EffectDeny, PolicyDigest: digest, Binding: policy.PolicyBinding{Version: 1, Digest: digest}}},
		Authorizer: policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
			if req.TargetState == policytest.StateFailed || req.TargetState == policytest.StatePassed {
				return policytest.AuthorizationDecision{}, nil
			}
			return a04Allowed(req), nil
		}),
	})
	if !errors.Is(err, policy.ErrAuthorizationDenied) {
		t.Fatalf("terminal authorization error = %v", err)
	}
	events, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == string(policytest.EventCasePassed) || event.Type == string(policytest.EventCaseFailed) {
			t.Fatalf("case event survived terminal authorization failure: %#v", event)
		}
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil || got.State != policytest.StateExecuted {
		t.Fatalf("run after terminal authorization failure = %#v, err=%v", got, err)
	}
}
