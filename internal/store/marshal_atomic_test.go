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

func TestMarshalPlanApprovalCannotReactivateSupersededPlan(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	project := projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")
	if err := s.InitProject(t.Context(), model.Project{ID: string(project), Repository: "canonical/repo", DefaultBranch: "main", PackVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	tasks := []plan.Task{{ID: "a", Title: "work", Criteria: []string{"done"}, Paths: []string{"a"}}}
	graph, err := plan.BuildGraph(tasks)
	if err != nil {
		t.Fatal(err)
	}
	oldPlan := plan.ExecutionPlan{ID: "old-plan", ProjectID: project, Goal: plan.GoalBinding{GoalID: "goal", Revision: 1}, Version: 1, State: plan.StateReady, Mode: plan.ModeStandard, Tasks: tasks, Graph: graph}
	run := marshal.Run{PlanID: oldPlan.ID, PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Drafting, Tasks: []marshal.Task{{PlanTaskID: "a", State: marshal.Queued}}}
	if err := s.SaveMarshalPlanState(t.Context(), string(project), "run", oldPlan, 0, run, 0, nil); err != nil {
		t.Fatal(err)
	}
	current := oldPlan
	current.ID = "new-plan"
	if err := s.SavePlan(t.Context(), current, 0); err != nil {
		t.Fatal(err)
	}
	approvedOld := oldPlan
	approvedOld.Version = 2
	approvedOld.State = plan.StateApproved
	run.PlanVersion = 2
	run.State = marshal.Approved
	if err := s.SaveMarshalPlanState(t.Context(), string(project), "run", approvedOld, 1, run, 1, nil); !errors.Is(err, plan.ErrPlanConflict) {
		t.Fatalf("superseded plan approval = %v, want active-plan conflict", err)
	}
	active, err := s.GetActivePlan(t.Context(), project)
	if err != nil || active.ID != current.ID || active.Version != 1 {
		t.Fatalf("active plan changed: %+v %v", active, err)
	}
	if _, err := s.GetPlan(t.Context(), oldPlan.ID, 2); !errors.Is(err, plan.ErrPlanNotFound) {
		t.Fatalf("superseded plan version escaped rollback: %v", err)
	}
	saved, err := s.GetMarshalRun(t.Context(), string(project), "run")
	if err != nil || saved.Revision != 1 || saved.Value.State != marshal.Drafting {
		t.Fatalf("run escaped rollback: %+v %v", saved, err)
	}
}

func TestMarshalImportRoutingCommitsWithPlanAndRun(t *testing.T) {
	s := openTestStore(t)
	project := projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")
	if err := s.InitProject(t.Context(), model.Project{ID: string(project), Repository: "canonical/repo", DefaultBranch: "main", PackVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	tasks := []plan.Task{{ID: "a", Title: "work", Criteria: []string{"done"}, Paths: []string{"a"}}}
	graph, err := plan.BuildGraph(tasks)
	if err != nil {
		t.Fatal(err)
	}
	p := plan.ExecutionPlan{ID: "plan", ProjectID: project, Goal: plan.GoalBinding{GoalID: "goal", Revision: 1}, Version: 1, State: plan.StateReady, Mode: plan.ModeStandard, Tasks: tasks, Graph: graph, Routes: map[string]plan.Route{"a": {Provider: "claude", Model: "selected"}}, RevisionReason: `task "a" worker reassigned: requested provider="codex" model="requested"; selected provider="claude" model="selected"; reason="provider cooldown: quota exceeded; resets at 10:00"`}
	run := marshal.Run{PlanID: p.ID, PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Drafting, Tasks: []marshal.Task{{PlanTaskID: "a", Worker: "claude", State: marshal.Queued}}}
	// Fail after the provenance insert, at transaction commit's final run event.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_import BEFORE INSERT ON structured_events BEGIN SELECT RAISE(ABORT,'injected import failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMarshalPlanState(t.Context(), string(project), "run", p, 0, run, 0, nil); err == nil {
		t.Fatal("failed import succeeded")
	}
	if _, err := s.GetMarshalRun(t.Context(), string(project), "run"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("run escaped rollback: %v", err)
	}
	if _, err := s.GetPlan(t.Context(), p.ID, 1); !errors.Is(err, plan.ErrPlanNotFound) {
		t.Fatalf("plan escaped rollback: %v", err)
	}
	if n := queryInt(t, s.db, `SELECT count(*) FROM structured_events WHERE event_id LIKE 'marshal-import-%'`); n != 0 {
		t.Fatal("misleading committed provenance")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_import`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMarshalPlanState(t.Context(), string(project), "run", p, 0, run, 0, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.MarshalImportProvenance(t.Context(), string(project), "run")
	if err != nil || len(got) != 1 {
		t.Fatalf("readback: %+v %v", got, err)
	}
	d := got[0]
	if d.RequestedProvider != "codex" || d.RequestedModel != "requested" || d.SelectedProvider != "claude" || d.SelectedModel != "selected" || d.Task != "a" || d.PlanID != p.ID || d.PlanVersion != 1 || d.Reason != "provider cooldown: quota exceeded; resets at 10:00" {
		t.Fatalf("lost provenance: %+v", d)
	}
	history, err := s.MarshalDecisions(t.Context(), "run")
	if err != nil || len(history) != 1 || history[0].TaskID != "a" || history[0].RunID != "run" || history[0].Subject != string(project) {
		t.Fatalf("uncorrelated timeline: %+v %v", history, err)
	}
	if _, err := s.MarshalImportProvenance(t.Context(), "foreign", "run"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("foreign read: %v", err)
	}
}
