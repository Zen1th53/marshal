//go:build !governed

package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// An operator's provider switch rebinds the lane parent's task within the
// same plan version: the next attempt opens bound to the new provider and
// model, needs a fresh task approval, and the parent's binding (which later
// lanes, dependents and completion compare) is the switched one.
func TestExtremeProviderSwitchRebindsNextLaneWithinPlanVersion(t *testing.T) {
	e, parent, base := laneFixture(t)
	registerWritingHarness(e)
	e.RegisterHarness(NewMockHarness("mock2", func(_ context.Context, task TaskExecution, _ ConstraintPackage, path string) (TaskResult, error) {
		if err := os.WriteFile(filepath.Join(path, task.TargetFiles[0]), []byte("switched "+task.RunID), 0600); err != nil {
			return TaskResult{}, err
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	first, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	executeLane(t, e, first, base)
	current := mustRun(t, e, parent.RunID)
	from := current.Tasks["task-1"].AssignedHarness
	allow := func(ExecutionRun, TaskExecution) error { return nil }
	if _, err = e.RebindTaskLaneProviderSwitch(t.Context(), parent.RunID, current.Version, "task-1", "other-source", "mock2", "model-2", "receipt-1", allow); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("switch from a provider the task does not have was accepted: %v", err)
	}
	refused := errors.New("scope changed")
	if _, err = e.RebindTaskLaneProviderSwitch(t.Context(), parent.RunID, current.Version, "task-1", from, "mock2", "model-2", "receipt-1", func(ExecutionRun, TaskExecution) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("an unauthorized switch was applied: %v", err)
	}
	if got := mustRun(t, e, parent.RunID); got.Tasks["task-1"].AssignedHarness != from || len(got.TaskProviderSwitches) != 0 {
		t.Fatal("a refused switch changed the parent")
	}
	switched, err := e.RebindTaskLaneProviderSwitch(t.Context(), parent.RunID, current.Version, "task-1", from, "mock2", "model-2", "receipt-1", allow)
	if err != nil {
		t.Fatal(err)
	}
	task := switched.Tasks["task-1"]
	if task.AssignedHarness != "mock2" || task.AssignedModel != "model-2" || !task.ApprovalRequired || task.ApprovalID != "" || switched.PlanVersion != parent.PlanVersion {
		t.Fatalf("switched task binding: %+v", task)
	}
	if record := switched.TaskProviderSwitches["task-1"]; record.From != from || record.To != "mock2" || record.Receipt != "receipt-1" || len(record.PriorBindings) != 1 || record.PriorBindings[0] != first.ParentBinding {
		t.Fatalf("switch record: %+v", record)
	}
	if replay, err := e.RebindTaskLaneProviderSwitch(t.Context(), parent.RunID, switched.Version, "task-1", from, "mock2", "model-2", "receipt-1", allow); err != nil || replay.Version != switched.Version {
		t.Fatalf("switch replay was not idempotent: %v", err)
	}
	if _, err = e.RebindTaskLaneProviderSwitch(t.Context(), parent.RunID, switched.Version, "task-1", from, "mock2", "model-2", "receipt-2", allow); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("another decision replayed the switch: %v", err)
	}
	second, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 1)
	if err != nil {
		t.Fatalf("the switched attempt did not open after the superseded one: %v", err)
	}
	laneTask := second.Tasks["task-1"]
	latest := mustRun(t, e, parent.RunID)
	if laneTask.AssignedHarness != "mock2" || laneTask.AssignedModel != "model-2" || second.ParentBinding != taskLaneBinding(latest, latest.Tasks["task-1"], base) || second.ParentBinding == first.ParentBinding {
		t.Fatalf("switched lane binding differs: %+v", laneTask)
	}
	if !ProviderSwitchAssigns(latest, laneTask) {
		t.Fatal("the switched lane task is not the recorded switch")
	}
	if err = e.ValidateTaskLaneBinding(t.Context(), second.RunID); err != nil {
		t.Fatalf("switched lane binding is not valid: %v", err)
	}
	// The switched provider has no approval: its first admission asks for a
	// task approval bound to the new provider.
	waiting, err := e.ExecuteTaskBound(t.Context(), second.RunID, "task-1", base)
	if err != nil || waiting.State != RunNeedsApproval || waiting.Tasks["task-1"].ApprovalID == "" {
		t.Fatalf("switched attempt did not ask for a fresh task approval: %v", err)
	}
	if err = e.DecideApproval(t.Context(), waiting.Tasks["task-1"].ApprovalID, true, "fixture-operator", "switched provider reviewed"); err != nil {
		t.Fatal(err)
	}
	done := executeLane(t, e, second, base)
	if done.Tasks["task-1"].AssignedHarness != "mock2" {
		t.Fatal("the switched attempt did not run on the new provider")
	}
	if retained := mustRun(t, e, first.RunID); retained.Tasks["task-1"].ResultCommit == "" {
		t.Fatal("the superseded attempt's evidence was not retained")
	}
	if ProviderSwitchAssigns(latest, first.Tasks["task-1"]) {
		t.Fatal("the superseded attempt was treated as the switched one")
	}
}
