package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestReconciliationIsReadOnlyAndReportsSplitBrain(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{ID: "TASK-001", Title: "runtime", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(repo.Path(), "file-state.json")
	original := []byte(`{"task":{"id":"TASK-OTHER","status":"working"},"project":{"branch":"other","commit":"deadbeef"}}`)
	if err := os.WriteFile(statePath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := runtime.Reconcile(context.Background(), app.ReconcileRequest{FileState: statePath})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "CONFLICT" || len(report.Conflicts) < 3 {
		t.Fatalf("report = %#v", report)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("reconciliation modified file state")
	}
	task, err := runtime.Task(context.Background(), "TASK-001")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskReady || task.Revision != 0 {
		t.Fatalf("reconciliation modified runtime state: %#v", task)
	}
}
