package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func marshalBoundBudgetFixture(t *testing.T, budget marshal.Budget) (*MarshalService, plan.ExecutionPlan) {
	t.Helper()
	ctx := t.Context()
	s, _ := marshalUserCloseFixture(t, 1)
	runtime := &Runtime{store: s.Store, layout: project.Layout{Root: s.Repository, Worktrees: s.Worktrees}}
	record, err := s.Store.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-budget", "SESSION-budget", record.ID
	if err := s.Store.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	request := planCreateRequest()
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(record.ID)
	p, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"fix": {"test -f README.md"}}
	if err := s.Store.SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	p, err = runtime.Plans().Approve(ctx, projectid.ID(record.ID))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.Budget = budget
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers = map[string]driver.Driver{"test-harness": driver.Governed{Provider: "test-harness"}}
	s.InstalledVersion = func(context.Context, string) string { return "1.0" }
	if err := s.Store.SaveHarnessProfile(ctx, model.HarnessProfile{Harness: "test-harness", InstalledVersion: "1.0", ProbeEvidenceID: "budget-fixture", ProbedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	return s, p
}

func TestMarshalApprovedPlanSnapshotsExactBudgetWithoutChangingExistingRun(t *testing.T) {
	for name, budget := range map[string]marshal.Budget{"unlimited": {}, "bounded": {Tokens: marshal.Ceiling{Task: 100, Plan: 500}, WallTime: marshal.Ceiling{Task: 300, Plan: 900}, Money: marshal.Ceiling{Task: 200, Plan: 800}}} {
		t.Run(name, func(t *testing.T) {
			s, p := marshalBoundBudgetFixture(t, budget)
			run, err := s.BindApprovedPlan(t.Context(), "bound-budget")
			if err != nil {
				t.Fatal(err)
			}
			if run.Budget != budget {
				t.Fatalf("bound budget=%+v; settings=%+v", run.Budget, budget)
			}
			saved, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "bound-budget")
			if err != nil || saved.Value.Budget != budget {
				t.Fatalf("durable budget=%+v err=%v", saved.Value.Budget, err)
			}
			if run.ApprovalScopeDigest != marshalApprovalDigest(p.ApprovalScopeDigest, run) {
				t.Fatal("approval digest omits bound budget")
			}
			changed := run
			changed.Budget.WallTime.Task++
			if marshalApprovalDigest(p.ApprovalScopeDigest, changed) == run.ApprovalScopeDigest {
				t.Fatal("changed bound budget retains approval digest")
			}
			settings, err := s.Store.GetMarshalSettings(t.Context(), s.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			settings.Value.Budget.WallTime.Task = 1
			if _, err := s.Store.SetMarshalSettings(t.Context(), s.ProjectID, settings.Value, settings.Revision); err != nil {
				t.Fatal(err)
			}
			saved, err = s.Store.GetMarshalRun(t.Context(), s.ProjectID, "bound-budget")
			if err != nil || saved.Value.Budget != budget || saved.Revision != 1 {
				t.Fatalf("settings edit changed existing run: %+v %v", saved, err)
			}
			newRun, err := s.BindApprovedPlan(t.Context(), "new-bound-budget")
			if err != nil || newRun.Budget != settings.Value.Budget {
				t.Fatalf("new run did not bind new settings: %+v %v", newRun.Budget, err)
			}
		})
	}
}

func TestMarshalApprovedPlanPropagatesTaskDeadlineToDriver(t *testing.T) {
	s, _ := marshalBoundBudgetFixture(t, marshal.Budget{WallTime: marshal.Ceiling{Task: 1, Plan: 10}})
	if _, err := s.BindApprovedPlan(t.Context(), "deadline-budget"); err != nil {
		t.Fatal(err)
	}
	observed := make(chan time.Time, 1)
	stopped := make(chan error, 1)
	s.GovernedDrivers["test-harness"] = driver.Governed{Provider: "test-harness", Run: func(ctx context.Context, _ driver.Request) ([]marshal.CommandRecord, error) {
		deadline, _ := ctx.Deadline()
		observed <- deadline
		<-ctx.Done()
		stopped <- ctx.Err()
		return nil, ctx.Err()
	}}
	start := time.Now()
	dispatch, err := s.Dispatch(t.Context(), "deadline-budget", "fix", "approved task")
	if err != nil {
		t.Fatal(err)
	}
	defer dispatch.Driver.Cancel(dispatch.Handle)
	select {
	case deadline := <-observed:
		if deadline.IsZero() || deadline.Sub(start) > 1500*time.Millisecond || deadline.Sub(start) < 500*time.Millisecond || !deadline.Equal(dispatch.Deadline) {
			t.Fatalf("actual driver deadline=%v dispatch=%v", deadline, dispatch.Deadline)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("driver did not launch")
	}
	select {
	case err := <-stopped:
		if err != context.DeadlineExceeded {
			t.Fatalf("driver stop=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bounded task context did not expire")
	}
}
