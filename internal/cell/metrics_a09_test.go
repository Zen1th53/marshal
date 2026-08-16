package cell

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestA09ObservedPrepareRecordsCellSuccessAndDuration(t *testing.T) {
	metrics := evidence.NewMetricsRecorder()
	repository := &memoryCellRepository{}
	backend := &countingCellBackend{}
	manager := NewObservedManager(repository, map[BackendKind]Backend{BackendNative: backend}, allowingCellAuthorizer{}, metrics)

	if _, err := manager.Prepare(context.Background(), Spec{
		TaskID: "TASK-cell-a09", Workspace: "/tmp/cell-a09", Backend: BackendNative,
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	snapshot := metrics.Snapshot()
	if got := snapshot.Success[evidence.MetricOperationCell]; got != 1 {
		t.Fatalf("cell successes=%d, want 1", got)
	}
	if got := snapshot.Active[evidence.MetricOperationCell]; got != 0 {
		t.Fatalf("active cells=%d, want 0 after preparation", got)
	}
	if got := snapshot.DurationNanoseconds[evidence.MetricOperationCell]; got == 0 {
		t.Fatal("cell duration was not recorded")
	}
}

func TestA09ObservedPrepareClassifiesDeniedAndInvalidRequests(t *testing.T) {
	deniedMetrics := evidence.NewMetricsRecorder()
	deniedManager := NewObservedManager(&memoryCellRepository{}, map[BackendKind]Backend{}, denyingCellAuthorizer{err: errors.New("denied")}, deniedMetrics)
	if _, err := deniedManager.Prepare(context.Background(), Spec{
		TaskID: "TASK-cell-a09-denied", Workspace: "/tmp/cell-a09-denied", Backend: BackendNative,
	}); err == nil {
		t.Fatal("denied preparation unexpectedly succeeded")
	}
	denied := deniedMetrics.Snapshot()
	if got := denied.Denied[string(CodeAuthorizationDenied)]; got != 1 {
		t.Fatalf("authorization denials=%d, want 1", got)
	}

	invalidMetrics := evidence.NewMetricsRecorder()
	invalidManager := NewObservedManager(&memoryCellRepository{}, map[BackendKind]Backend{}, allowingCellAuthorizer{}, invalidMetrics)
	if _, err := invalidManager.Prepare(context.Background(), Spec{
		TaskID: "TASK-cell-a09-invalid", Workspace: "/tmp/../escape", Backend: BackendNative,
	}); err == nil {
		t.Fatal("invalid preparation unexpectedly succeeded")
	}
	invalid := invalidMetrics.Snapshot()
	if got := invalid.Invalid[string(CodeScopeEscape)]; got != 1 {
		t.Fatalf("scope-invalid requests=%d, want 1", got)
	}
}
