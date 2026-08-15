package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

func TestA09PolicyPersistenceMetricsUseBoundedOperations(t *testing.T) {
	ctx := context.Background()
	metrics := evidence.NewMetricsRecorder()
	st, err := OpenWithObservability(ctx, t.TempDir()+"/policy.db", evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), nil, metrics)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "A09-METRICS")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version); err != nil {
		t.Fatal(err)
	}
	request := authorizedRequest(record)
	if _, err := st.TransitionPolicyStateAuthorized(ctx, request, policyMutationAuthorizer(func(context.Context, policy.PolicyMutationRequest) (policy.PolicyMutationDecision, error) {
		return allowedDecision(request), nil
	})); err != nil {
		t.Fatal(err)
	}
	snapshot := metrics.Snapshot()
	if got := snapshot.Success[evidence.MetricOperationPolicyPersist]; got != 1 {
		t.Fatalf("policy persistence successes = %d, want 1", got)
	}
	if got := snapshot.Success[evidence.MetricOperationPolicyLoad]; got != 2 {
		t.Fatalf("policy load successes = %d, want 2", got)
	}
	if got := snapshot.Success[evidence.MetricOperationPolicyTransition]; got != 1 {
		t.Fatalf("policy transition successes = %d, want 1", got)
	}
	if len(snapshot.Denied)+len(snapshot.Invalid)+len(snapshot.Conflict)+len(snapshot.Errors) != 0 {
		t.Fatalf("unexpected policy failure metrics: %#v", snapshot)
	}
}

func TestA09PolicyTestRunMetricsExposeBoundedOutcomeAndDuration(t *testing.T) {
	ctx := context.Background()
	metrics := evidence.NewMetricsRecorder()
	st, err := OpenWithObservability(ctx, filepath.Join(t.TempDir(), "policy-test.db"), evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), nil, metrics)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	p := policy.Policy{ID: "A09-TEST-METRICS", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	suite := a06Suite(t, p, digest)
	run := policytest.TestRun{
		ID: "run-a09-metrics", PolicyID: p.ID, Binding: suite.Cases[0].Given.Binding,
		TestFileDigest: digest,
		Cases:          []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}},
	}
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	request := policytest.RunRequest{
		RunID: run.ID, Suite: suite, TestFileDigest: digest,
		SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1",
		Evaluator: &a06Evaluator{decision: policy.Decision{Effect: policy.EffectDeny, PolicyDigest: digest, Binding: policy.PolicyBinding{Version: 1, Digest: digest}}},
		Authorizer: policyTestAuthorizer(func(_ context.Context, req policytest.AuthorizationRequest) (policytest.AuthorizationDecision, error) {
			return a04Allowed(req), nil
		}),
	}
	result, err := st.RunPolicyTest(ctx, request)
	if err != nil || result.Status != policytest.StatusPass {
		t.Fatalf("RunPolicyTest result=%#v err=%v", result, err)
	}
	snapshot := metrics.Snapshot()
	if got := snapshot.Success[evidence.MetricOperationPolicyTest]; got != 1 {
		t.Fatalf("policy-test successes = %d, want 1", got)
	}
	if got := snapshot.Observations[evidence.MetricOperationPolicyTest]; got != 1 {
		t.Fatalf("policy-test observations = %d, want 1", got)
	}
	if got := snapshot.DurationNanoseconds[evidence.MetricOperationPolicyTest]; got == 0 {
		t.Fatal("policy-test duration was not recorded")
	}
	if got := snapshot.Active[evidence.MetricOperationPolicyTest]; got != 0 {
		t.Fatalf("active policy claims after terminal result = %d, want 0", got)
	}
}

func TestA09PolicyTestInvalidRequestIncrementsInvalidMetric(t *testing.T) {
	ctx := context.Background()
	metrics := evidence.NewMetricsRecorder()
	st, err := OpenWithObservability(ctx, filepath.Join(t.TempDir(), "policy-test-invalid.db"), evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), nil, metrics)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RunPolicyTest(ctx, policytest.RunRequest{RunID: "invalid", TestFileDigest: "not-a-digest"}); err == nil {
		t.Fatal("invalid policy-test request unexpectedly succeeded")
	}
	if got := metrics.Snapshot().Invalid["POLICY_ERROR"]; got != 1 {
		t.Fatalf("invalid policy-test requests = %d, want 1", got)
	}
}
