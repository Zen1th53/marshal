package authz

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestObservedAuthorityMetricsAreBoundedAndNonAuthoritative(t *testing.T) {
	metrics := evidence.NewMetricsRecorder()
	principal := Principal{ID: "agent-a09", Role: Role{Name: "developer", Authorities: []Authority{AuthoritySourceWrite}}}
	decision, err := CanObserved(context.Background(), principal, AuthoritySourceWrite, "repo:a09", metrics)
	if err != nil || !decision.Allowed {
		t.Fatalf("allow decision=%#v err=%v", decision, err)
	}
	denied, err := CanObserved(context.Background(), principal, AuthorityPolicyAdmin, "repo:a09", metrics)
	if err == nil || denied.Allowed {
		t.Fatalf("deny decision=%#v err=%v", denied, err)
	}
	snapshot := metrics.Snapshot()
	if snapshot.Success[evidence.MetricOperationAuthority] != 1 {
		t.Fatalf("allow count=%d", snapshot.Success[evidence.MetricOperationAuthority])
	}
	if snapshot.Denied[string(CodeDenied)] != 1 {
		t.Fatalf("deny count=%d", snapshot.Denied[string(CodeDenied)])
	}
	if snapshot.DurationNanoseconds[evidence.MetricOperationAuthority] == 0 {
		t.Fatal("authority duration missing")
	}
	if got := len(snapshot.Denied); got != 1 {
		t.Fatalf("unbounded denied reasons=%d", got)
	}
}

func TestObservedAuthorityMetricsDoNotTurnCancellationIntoAllow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	metrics := evidence.NewMetricsRecorder()
	principal := Principal{ID: "agent-a09", Role: Role{Name: "developer", Authorities: []Authority{AuthoritySourceWrite}}}
	decision, err := CanObserved(ctx, principal, AuthoritySourceWrite, "repo:a09", metrics)
	if err == nil || decision.Allowed {
		t.Fatalf("cancelled decision=%#v err=%v", decision, err)
	}
	if got := metrics.Snapshot().Success[evidence.MetricOperationAuthority]; got != 0 {
		t.Fatalf("cancelled allow count=%d", got)
	}
}

func BenchmarkCanObserved(b *testing.B) {
	metrics := evidence.NewMetricsRecorder()
	principal := Principal{ID: "agent-benchmark", Role: Role{Name: "developer", Authorities: []Authority{AuthoritySourceWrite}}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CanObserved(context.Background(), principal, AuthoritySourceWrite, "repo:benchmark", metrics); err != nil {
			b.Fatal(err)
		}
	}
}
