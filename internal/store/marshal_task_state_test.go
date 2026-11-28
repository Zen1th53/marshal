package store

import (
	"github.com/Zen1th53/marshal/internal/marshal"
	"testing"
)

func TestTaskSnapshotWritersRefuseOffTableMoves(t *testing.T) {
	for _, writer := range []string{"run", "task", "atomic"} {
		t.Run(writer, func(t *testing.T) {
			s := openTestStore(t)
			marshalTestProject(t, s)
			run := marshal.Run{PlanID: "plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "task", State: marshal.Queued}}}
			if err := s.SaveMarshalState(t.Context(), "p", "run", run, 0, nil, nil, 0); err != nil {
				t.Fatal(err)
			}
			run.Tasks[0].State = marshal.Merged
			var err error
			switch writer {
			case "run":
				_, err = s.SetMarshalRun(t.Context(), "p", "run", run, 1)
			case "task":
				_, err = s.SetMarshalTask(t.Context(), "run", run.Tasks[0], 1)
			case "atomic":
				err = s.SaveMarshalState(t.Context(), "p", "run", run, 1, nil, nil, 0)
			}
			if err == nil {
				t.Fatal("queued task skipped directly to merged")
			}
			saved, err := s.GetMarshalRun(t.Context(), "p", "run")
			if err != nil {
				t.Fatal(err)
			}
			task, err := s.GetMarshalTask(t.Context(), "run", "task")
			if err != nil {
				t.Fatal(err)
			}
			if saved.Revision != 1 || task.Revision != 1 || saved.Value.Tasks[0].State != marshal.Queued || task.Value.State != marshal.Queued {
				t.Fatal("refused write changed durable state")
			}
		})
	}
}

// Whole-struct copies are allowed by the source guard; persistence validates their results.
func TestNewPlanVersionRefusesTerminalTaskOverwrite(t *testing.T) {
	for _, state := range []marshal.TaskState{marshal.Merged, marshal.Cancelled} {
		t.Run(string(state), func(t *testing.T) {
			s := openTestStore(t)
			marshalTestProject(t, s)
			run := marshal.Run{PlanID: "plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "task", State: state}}}
			if err := s.SaveMarshalState(t.Context(), "p", "run", run, 0, nil, nil, 0); err != nil {
				t.Fatal(err)
			}
			run.PlanVersion++
			run.Tasks[0] = marshal.Task{PlanTaskID: "task", State: marshal.Queued}
			if err := s.SaveMarshalState(t.Context(), "p", "run", run, 1, nil, nil, 0); err == nil {
				t.Fatal("terminal task overwritten")
			}
			saved, err := s.GetMarshalRun(t.Context(), "p", "run")
			if err != nil || saved.Value.Tasks[0].State != state || saved.Revision != 1 {
				t.Fatalf("durable state changed: %+v %v", saved, err)
			}
		})
	}
}

func TestTerminalAmendmentSnapshotGuard(t *testing.T) {
	for _, state := range []marshal.TaskState{marshal.Merged, marshal.Cancelled} {
		for _, tc := range []struct {
			name         string
			plan         int64
			branch, work string
			nextState    marshal.TaskState
			allowed      bool
		}{
			{"replacement", 2, "marshal/run/r2/task", "new", marshal.Queued, true},
			{"same artifact", 2, "marshal/run/r1/task", "new", marshal.Queued, false},
			{"older artifact", 2, "marshal/run/task", "new", marshal.Queued, false},
			{"same plan", 1, "marshal/run/r2/task", "new", marshal.Queued, false},
			{"same definition", 2, "marshal/run/r2/task", "old", marshal.Queued, false},
			{"other open state", 2, "marshal/run/r2/task", "new", marshal.Dispatched, false},
		} {
			t.Run(string(state)+"/"+tc.name, func(t *testing.T) {
				s := openTestStore(t)
				marshalTestProject(t, s)
				run := marshal.Run{PlanID: "plan", PlanVersion: 1, ArtifactRevision: 1, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "task", State: state, Branch: "marshal/run/r1/task", Instructions: "old"}}}
				if err := s.SaveMarshalState(t.Context(), "p", "run", run, 0, nil, nil, 0); err != nil {
					t.Fatal(err)
				}
				run.PlanVersion, run.ArtifactRevision = tc.plan, 2
				run.Tasks[0].State, run.Tasks[0].Branch, run.Tasks[0].Instructions = tc.nextState, tc.branch, tc.work
				err := s.SaveMarshalState(t.Context(), "p", "run", run, 1, nil, nil, 0)
				if (err == nil) != tc.allowed {
					t.Fatalf("save: %v", err)
				}
				saved, err := s.GetMarshalTask(t.Context(), "run", "task")
				wantState, wantRevision := state, int64(1)
				if tc.allowed {
					wantState, wantRevision = marshal.Queued, 2
				}
				if err != nil || saved.Value.State != wantState || saved.Revision != wantRevision {
					t.Fatalf("task projection: %+v %v", saved, err)
				}
			})
		}
	}
}
