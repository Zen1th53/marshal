//go:build governed

package app

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Community keeps serial governed dispatch: a task binding never makes
// dispatch pass a task by, and an idle run is never paused as an approval wait.
func TestCommunityHasNoParallelProcess05ApprovalWaits(t *testing.T) {
	s, _ := marshalFixture(t, 2)
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Process05Bound, run.Process05Parallel, run.Tier = true, true, marshal.Ultra
	run.Tasks[0].Process05WaitRunID, run.Tasks[0].Process05WaitApprovalID = "lane", "approval"
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	if waiting, err := s.privateProcess05WaitPending(t.Context(), run, run.Tasks[0]); err != nil || waiting {
		t.Fatalf("community passed a task by for a wait: %v %v", waiting, err)
	}
	if again, err := s.pausePrivateProcess05Waits(t.Context(), "run"); err != nil || again {
		t.Fatalf("community paused for a wait: %v %v", again, err)
	}
	after, _, err := s.load(t.Context(), "run")
	if err != nil || after.State == marshal.AwaitingUser {
		t.Fatalf("community run changed: %s %v", after.State, err)
	}
	if handled, err := s.pausePrivateProcess05Approval(t.Context(), "run", MarshalDispatch{TaskID: "a"}, process05ApprovalRequired("lane", "a", "approval")); handled || err != nil {
		t.Fatalf("community handled an approval error as a wait: %v %v", handled, err)
	}
}
