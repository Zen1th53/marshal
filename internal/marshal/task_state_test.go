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
		if state == Merged || state == Cancelled {
			if ResetTaskForAmendment(&task) == nil || task.State != state {
				t.Fatal("terminal reset accepted")
			}
			continue
		}
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

func TestAmendmentRestoreIsBoundToPreviousState(t *testing.T) {
	previous := Task{PlanTaskID: "task", State: Returned}
	for state := range taskMoves {
		if state == previous.State {
			continue
		}
		task := Task{PlanTaskID: "task", State: Queued}
		if setTaskState(&task, state, &previous, false) == nil || task.State != Queued {
			t.Fatalf("restored %s absent from previous snapshot", state)
		}
	}
}

func TestTerminalAmendmentRequiresChangedDefinitionAndArtifact(t *testing.T) {
	for _, state := range []TaskState{Merged, Cancelled} {
		for _, tc := range []struct {
			name, instructions, branch string
			allowed                    bool
		}{
			{"replacement", "new work", "marshal/run/r2/task", true},
			{"same artifact", "new work", "marshal/run/r1/task", false},
			{"older artifact", "new work", "marshal/run/task", false},
			{"unchanged definition", "old work", "marshal/run/r2/task", false},
			{"other run", "new work", "marshal/other/r2/task", false},
		} {
			t.Run(string(state)+"/"+tc.name, func(t *testing.T) {
				old := Task{PlanTaskID: "task", State: state, Instructions: "old work", Branch: "marshal/run/r1/task"}
				next := old
				next.Instructions, next.Branch = tc.instructions, tc.branch
				err := ResetTaskForAmendment(&next, old)
				if (err == nil) != tc.allowed {
					t.Fatalf("reset: %v", err)
				}
				if tc.allowed && next.State != Queued || !tc.allowed && next.State != state {
					t.Fatalf("unexpected state: %s", next.State)
				}
			})
		}
	}
}
