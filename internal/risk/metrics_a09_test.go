package risk

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestObservedRiskAssessmentMetricsAreBoundedAndNonAuthoritative(t *testing.T) {
	metrics := evidence.NewMetricsRecorder()
	engine := NewObservedEngine(&memoryAssessmentStore{}, nil, metrics)
	if _, err := engine.Assess(context.Background(), AssessmentRequest{
		ID:         "assessment-a09",
		Descriptor: ToolDescriptor{Tool: "go", Action: "test", Resource: "repo:marshal"},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := metrics.Snapshot()
	if snapshot.Success[evidence.MetricOperationRisk] != 1 || snapshot.DurationNanoseconds[evidence.MetricOperationRisk] == 0 {
		t.Fatalf("risk metrics = %#v", snapshot)
	}
	metrics.Observe(evidence.MetricOperationRisk, evidence.MetricResultDenied, "MARSHAL_TEST_SECRET_T24_A09", time.Microsecond)
	if got := metrics.Snapshot().LastFailureReason; got != "UNCLASSIFIED" {
		t.Fatalf("unbounded reason escaped metrics: %q", got)
	}
}
