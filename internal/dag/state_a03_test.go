package dag

import "testing"

func TestT29A03StateMachineAllowsOnlyCanonicalTransitions(t *testing.T) {
	allowed := map[[2]NodeStatus]bool{
		{StatusPending, StatusReady}:     true,
		{StatusReady, StatusRunning}:     true,
		{StatusRunning, StatusCompleted}: true,
		{StatusRunning, StatusFailed}:    true,
		{StatusRunning, StatusBlocked}:   true,
		{StatusRunning, StatusSkipped}:   true,
	}
	states := []NodeStatus{StatusPending, StatusReady, StatusRunning, StatusCompleted, StatusFailed, StatusBlocked, StatusSkipped}
	for _, from := range states {
		for _, to := range states {
			if got, want := CanTransition(from, to), allowed[[2]NodeStatus{from, to}]; got != want {
				t.Fatalf("CanTransition(%q,%q)=%v want=%v", from, to, got, want)
			}
		}
	}
	if CanTransition(NodeStatus("unknown"), StatusReady) || CanTransition(StatusPending, NodeStatus("unknown")) {
		t.Fatal("unknown state was accepted")
	}
}
