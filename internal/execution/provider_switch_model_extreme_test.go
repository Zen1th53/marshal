//go:build !governed

package execution

import (
	"errors"
	"testing"
)

// Extreme's flexible worker models: the lane parent's task moves to another
// model of the same provider; the next lane opens bound to it. A fallback
// (provider failover) still needs another provider.
func TestFlexibleModelsSameProviderModelSwitch(t *testing.T) {
	e, parent, base := laneFixture(t)
	registerWritingHarness(e)
	first, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	executeLane(t, e, first, base)
	current := mustRun(t, e, parent.RunID)
	provider := current.Tasks["task-1"].AssignedHarness
	allow := func(ExecutionRun, TaskExecution) error { return nil }
	switched, err := e.RebindTaskLaneProviderSwitch(t.Context(), parent.RunID, current.Version, "task-1", provider, provider, "model-b", "flex-1", allow)
	if err != nil {
		t.Fatalf("same-provider model switch refused: %v", err)
	}
	task := switched.Tasks["task-1"]
	if task.AssignedHarness != provider || task.AssignedModel != "model-b" || !task.ApprovalRequired || task.ApprovalID != "" {
		t.Fatalf("model switch binding: %+v", task)
	}
	if replay, err := e.RebindTaskLaneProviderSwitch(t.Context(), parent.RunID, switched.Version, "task-1", provider, provider, "model-b", "flex-1", allow); err != nil || replay.Version != switched.Version {
		t.Fatalf("model switch replay not idempotent: %v", err)
	}
	second, err := e.OpenTaskLaneRework(t.Context(), parent.RunID, "task-1", base, 1)
	if err != nil {
		t.Fatalf("switched model lane did not open: %v", err)
	}
	if got := second.Tasks["task-1"]; got.AssignedHarness != provider || got.AssignedModel != "model-b" {
		t.Fatalf("lane not bound to the new model: %+v", got)
	}
	if err := e.ValidateTaskLaneBinding(t.Context(), second.RunID); err != nil {
		t.Fatalf("model-switched lane binding invalid: %v", err)
	}
	if _, err := e.RebindTaskLaneApprovedProviderFailover(t.Context(), parent.RunID, mustRun(t, e, parent.RunID).Version, "task-1", provider, provider, base, "flex-2", allow); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("a same-provider fallback was accepted: %v", err)
	}
}
