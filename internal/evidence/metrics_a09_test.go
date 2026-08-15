package evidence

import (
	"fmt"
	"testing"
	"time"
)

func TestA09MetricsCardinalityRemainsBoundedForAttackerIdentifiers(t *testing.T) {
	recorder := NewMetricsRecorder()
	for i := 0; i < 1000; i++ {
		recorder.Observe(MetricOperationPolicyRuntimeGate, MetricResultDenied, fmt.Sprintf("subject-%d/resource-%d", i, i), time.Microsecond)
	}
	snapshot := recorder.Snapshot()
	if len(snapshot.Denied) != 1 {
		t.Fatalf("denial reason dimensions = %d, want 1", len(snapshot.Denied))
	}
	if snapshot.Denied["UNCLASSIFIED"] != 1000 {
		t.Fatalf("unclassified denials = %d, want 1000", snapshot.Denied["UNCLASSIFIED"])
	}
	if len(snapshot.Observations) != 1 || len(snapshot.DurationNanoseconds) != 1 {
		t.Fatalf("operation dimensions expanded: observations=%d durations=%d", len(snapshot.Observations), len(snapshot.DurationNanoseconds))
	}
}

func TestA09MetricsDoNotRetainSecretBearingErrorText(t *testing.T) {
	marker := "MARSHAL_TEST_SECRET_T48_A09_METRIC"
	recorder := NewMetricsRecorder()
	recorder.Observe(MetricOperationPolicyRuntimeGate, MetricResultError, marker, time.Millisecond)
	snapshot := recorder.Snapshot()
	if snapshot.LastFailureReason == marker {
		t.Fatal("secret marker retained as last failure reason")
	}
	if _, ok := snapshot.Errors[marker]; ok {
		t.Fatal("secret marker retained as error dimension")
	}
	if snapshot.Errors["UNCLASSIFIED"] != 1 {
		t.Fatalf("unclassified errors = %d, want 1", snapshot.Errors["UNCLASSIFIED"])
	}
}
