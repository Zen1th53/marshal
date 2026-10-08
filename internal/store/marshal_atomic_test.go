package store

import (
	"errors"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"testing"
	"time"
)

func TestMarshalStateRollsBackTasksAndEventTogether(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	run := marshal.Run{PlanID: "plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Approved, Tasks: []marshal.Task{{PlanTaskID: "a", State: marshal.Queued}}}
	if _, err := s.SetMarshalRun(t.Context(), "p", "run", run, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMarshalTask(t.Context(), "run", run.Tasks[0], 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_task BEFORE UPDATE ON marshal_tasks BEGIN SELECT RAISE(ABORT, 'injected interruption'); END`); err != nil {
		t.Fatal(err)
	}
	run.State = marshal.Dispatching
	run.Tasks[0].State = marshal.Dispatched
	event := events.Event{ID: "event", Type: events.EventTypeMarshalTaskDispatched, Subject: "p", RunID: "run", TaskID: "a", At: time.Now(), Data: map[string]any{}}
	if err := s.SaveMarshalState(t.Context(), "p", "run", run, 1, &event, nil, 0); err == nil {
		t.Fatal("injected fault ignored")
	}
	got, err := s.GetMarshalRun(t.Context(), "p", "run")
	if err != nil || got.Revision != 1 || got.Value.State != marshal.Approved {
		t.Fatalf("partial run save: %+v %v", got, err)
	}
	history, err := s.MarshalDecisions(t.Context(), "run")
	if err != nil || len(history) != 0 {
		t.Fatalf("partial event save: %v %v", history, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_task`); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveMarshalState(t.Context(), "p", "run", run, 1, &event, nil, 0); err != nil {
		t.Fatal(err)
	}
	task, err := s.GetMarshalTask(t.Context(), "run", "a")
	if err != nil || task.Value.State != marshal.Dispatched {
		t.Fatalf("task not saved: %v %v", task, err)
	}
	history, err = s.MarshalDecisions(t.Context(), "run")
	if err != nil || len(history) != 1 {
		t.Fatalf("completion event: %v %v", history, err)
	}
}

func TestMarshalPlanAndRunRollbackTogether(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	tasks := []plan.Task{{ID: "a", Title: "work", Criteria: []string{"done"}, Paths: []string{"a"}}}
	graph, err := plan.BuildGraph(tasks)
	if err != nil {
		t.Fatal(err)
	}
	p := plan.ExecutionPlan{ID: "plan", ProjectID: projectid.ID("PROJECT-0123456789abcdef0123456789abcdef"), Goal: plan.GoalBinding{GoalID: "goal", Revision: 1}, Version: 1, State: plan.StateReady, Mode: plan.ModeStandard, Tasks: tasks, Graph: graph}
	// Plans use canonical project identity, while this store fixture uses p.
	if err = s.InitProject(t.Context(), model.Project{ID: string(p.ProjectID), Repository: "canonical/repo", DefaultBranch: "main", PackVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	run := marshal.Run{PlanID: p.ID, PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Drafting, Tasks: []marshal.Task{{PlanTaskID: "a", State: marshal.Queued}}}
	if _, err = s.db.Exec(`CREATE TRIGGER reject_run BEFORE INSERT ON marshal_runs BEGIN SELECT RAISE(ABORT,'injected interruption'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveMarshalPlanState(t.Context(), string(p.ProjectID), "run", p, 0, run, 0, nil); err == nil {
		t.Fatal("injected failure ignored")
	}
	if _, err = s.GetPlan(t.Context(), p.ID, 1); !errors.Is(err, plan.ErrPlanNotFound) {
		t.Fatalf("plan escaped rollback: %v", err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER reject_run`); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveMarshalPlanState(t.Context(), string(p.ProjectID), "run", p, 0, run, 0, nil); err != nil {
		t.Fatal(err)
	}
}
