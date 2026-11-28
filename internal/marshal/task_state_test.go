package marshal

import "testing"

func TestSetTaskState(t *testing.T) {
	allowed := map[TaskState][]TaskState{
		Queued:     {Dispatched, Escalated, Reassigned},
		Dispatched: {HandedIn, Returned, Queued, Escalated, Reassigned},
		HandedIn:   {Accepted, Returned, Reassigned, Escalated},
		Accepted:   {Merged, Returned, Reassigned, Escalated}, Returned: {Dispatched, Reassigned, Escalated, Queued},
		Reassigned: {Dispatched, Escalated}, Escalated: {Returned, Reassigned, Cancelled}, Merged: {}, Cancelled: {},
	}
	states := []TaskState{"", Queued, Dispatched, HandedIn, Accepted, Returned, Reassigned, Escalated, Merged, Cancelled, "unknown"}
	for _, from := range states {
		for _, to := range states {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				task := Task{PlanTaskID: "task", State: from}
				want := false
				if _, known := allowed[to]; known {
					want = from == to || from == "" && to == Queued
					for _, next := range allowed[from] {
						want = want || next == to
					}
				}
				err := SetTaskState(&task, to)
				if (err == nil) != want {
					t.Fatalf("move %s -> %s: %v", from, to, err)
				}
				if want && task.State != to || !want && task.State != from {
					t.Fatalf("unexpected state %s", task.State)
				}
			})
		}
	}
	if SetTaskState(nil, Queued) == nil {
		t.Fatal("nil task accepted")
	}
}

func TestRestoreAmendedTaskState(t *testing.T) {
	for state := range taskMoves {
		task := Task{PlanTaskID: "task", State: Queued}
		if err := RestoreAmendedTaskState(&task, Task{PlanTaskID: "task", State: state}); err != nil || task.State != state {
			t.Fatalf("restore %s: %v", state, err)
		}
	}
	for _, tc := range []struct{ current, previous Task }{
		{Task{PlanTaskID: "task", State: Dispatched}, Task{PlanTaskID: "task", State: Merged}},
		{Task{PlanTaskID: "task", State: Queued}, Task{PlanTaskID: "other", State: Merged}},
		{Task{PlanTaskID: "task", State: Queued}, Task{PlanTaskID: "task", State: "unknown"}},
	} {
		before := tc.current.State
		if RestoreAmendedTaskState(&tc.current, tc.previous) == nil || tc.current.State != before {
			t.Fatal("invalid restoration accepted")
		}
	}
}

func TestResetTaskForAmendment(t *testing.T) {
	for state := range taskMoves {
		task := Task{PlanTaskID: "task", State: state}
		if err := ResetTaskForAmendment(&task); err != nil || task.State != Queued {
			t.Fatalf("reset %s: %v", state, err)
		}
	}
	task := Task{PlanTaskID: "task", State: "unknown"}
	if ResetTaskForAmendment(&task) == nil || task.State != "unknown" {
		t.Fatal("reset accepted unknown state")
	}
	if ResetTaskForAmendment(nil) == nil {
		t.Fatal("reset accepted nil task")
	}
}
