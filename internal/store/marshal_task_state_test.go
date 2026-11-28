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

func TestNewPlanVersionCanResetFinishedTask(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	run := marshal.Run{PlanID: "plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "task", State: marshal.Merged}}}
	if err := s.SaveMarshalState(t.Context(), "p", "run", run, 0, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].State = marshal.Queued
	if err := s.SaveMarshalState(t.Context(), "p", "run", run, 1, nil, nil, 0); err == nil {
		t.Fatal("finished task reset without new plan")
	}
	run.PlanVersion++
	if err := s.SaveMarshalState(t.Context(), "p", "run", run, 1, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
}
