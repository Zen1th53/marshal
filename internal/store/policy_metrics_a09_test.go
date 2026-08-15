package store

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/policy"
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
