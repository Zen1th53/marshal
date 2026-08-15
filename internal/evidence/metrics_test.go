package evidence

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMetricsRecorderUsesBoundedDimensions(t *testing.T) {
	recorder := NewMetricsRecorder()
	recorder.Observe(MetricOperationPutNode, MetricResultSuccess, "EVIDENCE-123", 4*time.Millisecond)
	recorder.Observe(MetricOperationTransition, MetricResultDenied, string(CodeAuthorizationDenied), 2*time.Millisecond)
	recorder.Observe(MetricOperationTransition, MetricResultDenied, "MARSHAL_TEST_SECRET_T06_A09_METRIC", time.Millisecond)

	snapshot := recorder.Snapshot()
	if snapshot.Success[MetricOperationPutNode] != 1 {
		t.Fatalf("put success = %d, want 1", snapshot.Success[MetricOperationPutNode])
	}
	if snapshot.Denied["EVIDENCE-123"] != 0 {
		t.Fatal("arbitrary reason was accepted as a metric dimension")
	}
	if snapshot.Denied[string(CodeAuthorizationDenied)] != 1 {
		t.Fatalf("authorization denials = %d, want 1", snapshot.Denied[string(CodeAuthorizationDenied)])
	}
	if snapshot.Denied[string(CodeAuthorizationStale)] != 0 {
		t.Fatal("unexpected stale denial")
	}
	if snapshot.LastFailureReason != "UNCLASSIFIED" {
		t.Fatalf("last failure reason = %q", snapshot.LastFailureReason)
	}
}

func TestMetricsRecorderIsConcurrentAndContextIndependent(t *testing.T) {
	recorder := NewMetricsRecorder()
	const workers = 32
	const iterations = 100
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				recorder.Observe(MetricOperationPutNode, MetricResultSuccess, "", time.Microsecond)
			}
		}()
	}
	wg.Wait()
	if got, want := recorder.Snapshot().Success[MetricOperationPutNode], uint64(workers*iterations); got != want {
		t.Fatalf("success count = %d, want %d", got, want)
	}
	if err := recorder.ObserveContext(context.Background(), MetricOperationGet, MetricResultSuccess, "", time.Nanosecond); err != nil {
		t.Fatal(err)
	}
}
