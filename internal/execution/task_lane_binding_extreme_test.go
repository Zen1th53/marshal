//go:build !governed

package execution

import "testing"

func TestExtremeTaskLaneSurfaceBindingRefusesChangedParentAndRoot(t *testing.T) {
	e, parent, base := laneFixture(t)
	lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ValidateTaskLaneBinding(t.Context(), lane.RunID); err != nil {
		t.Fatal(err)
	}
	canonical, err := e.store.GetRun(t.Context(), parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	changed := canonical
	original := changed.Tasks["task-1"]
	original.Description += " changed"
	changed.Tasks["task-1"] = original
	if err := e.store.UpdateRun(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	if err := e.ValidateTaskLaneBinding(t.Context(), lane.RunID); err == nil {
		t.Fatal("changed original task admitted")
	}
}

// When the last lane completes, its parent is DONE_PENDING_VERIFICATION. The
// historical binding of that finished lane stays valid for its review record;
// live-work validation and other ended parents still refuse.
func TestExtremeCompletedTaskLaneBindingAcceptsDoneParentOnly(t *testing.T) {
	e, parent, base := laneFixture(t)
	lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := e.store.GetRun(t.Context(), parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []RunState{RunDonePendingVerification, RunFailed, RunCancelled} {
		ended := canonical
		ended.State = state
		if err := e.store.UpdateRun(t.Context(), ended); err != nil {
			t.Fatal(err)
		}
		if err := e.ValidateTaskLaneBinding(t.Context(), lane.RunID); err == nil {
			t.Fatalf("live-work binding admitted a %s parent", state)
		}
		err := e.ValidateCompletedTaskLaneBinding(t.Context(), lane.RunID)
		if (state == RunDonePendingVerification) != (err == nil) {
			t.Fatalf("completed binding with %s parent: %v", state, err)
		}
		canonical, err = e.store.GetRun(t.Context(), parent.RunID)
		if err != nil {
			t.Fatal(err)
		}
	}
}
