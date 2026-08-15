package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestEvidenceMetricsRecordBoundedOperationOutcomes(t *testing.T) {
	ctx := context.Background()
	metrics := evidence.NewMetricsRecorder()
	st, err := OpenWithObservability(ctx, filepath.Join(t.TempDir(), "state.db"), evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), nil, metrics)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	node := testEvidenceNode("A09-METRICS", string(evidence.NodeTypeClaim), "metrics")
	if _, err := st.PutNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	conflict := testEvidenceNode(string(node.ID), string(evidence.NodeTypeClaim), "different")
	if _, err := st.PutNode(ctx, conflict); err == nil {
		t.Fatal("expected immutable conflict")
	}
	got := metrics.Snapshot()
	if got.Success[evidence.MetricOperationPutNode] != 1 {
		t.Fatalf("put successes = %d", got.Success[evidence.MetricOperationPutNode])
	}
	if got.Conflict[string(evidence.CodeImmutable)] != 1 {
		t.Fatalf("immutable conflicts = %d", got.Conflict[string(evidence.CodeImmutable)])
	}
	if got.LastFailureReason != string(evidence.CodeImmutable) {
		t.Fatalf("last failure = %q", got.LastFailureReason)
	}
}
