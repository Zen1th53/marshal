package app

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestTaskGoalDispatchAcceptsSettledLocalGoalOnly(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		confirmation          model.ConfirmationState
		foreign, stale, ended bool
		allowed               bool
	}{
		{name: "approved", confirmation: model.ConfirmationApproved, allowed: true},
		{name: "delegated", confirmation: model.ConfirmationDelegated, allowed: true},
		{name: "foreign-approved", confirmation: model.ConfirmationApproved, foreign: true},
		{name: "pending", confirmation: model.ConfirmationPending},
		{name: "cancelled", confirmation: model.ConfirmationCancelled},
		{name: "stale-approved", confirmation: model.ConfirmationApproved, stale: true},
		{name: "terminated-approved", confirmation: model.ConfirmationApproved, ended: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runtimeForPlan(t)
			ctx := t.Context()
			goal := planGoal()
			goal.ID, goal.SessionID = "GOAL-dispatch", "SESSION-dispatch"
			goal.ProjectID, goal.Confirmation = r.ProjectIdentity(), tc.confirmation
			if tc.foreign {
				goal.ProjectID = string(runtimePlanProject)
				if goal.ProjectID == r.ProjectIdentity() {
					t.Fatal("foreign fixture must differ from runtime identity")
				}
			}
			if err := r.store.SaveGoalContract(ctx, goal, 0); err != nil {
				t.Fatal(err)
			}
			binding := store.TaskBinding{SessionID: goal.SessionID, GoalID: goal.ID, GoalRevision: goal.Revision}
			const taskID = "TASK-dispatch"
			if _, err := r.ImportTasks(store.WithTaskBinding(ctx, binding), []model.Task{{ID: taskID, Title: "bounded work", Status: model.TaskReady, Risk: model.R1}}); err != nil {
				t.Fatal(err)
			}
			if tc.stale {
				if err := r.store.SaveGoalContract(ctx, goal, goal.Revision); err != nil {
					t.Fatal(err)
				}
			}
			if tc.ended {
				if err := r.store.SaveGoalTermination(ctx, model.GoalTermination{SessionID: goal.SessionID, GoalID: goal.ID, GoalRevision: goal.Revision, State: model.StateCancelled, ReasonCode: model.ReasonUserCancelled, CompletedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			}
			// Validate at the task and actual launch boundaries. A rejected goal
			// must never obtain launch admission, even if it is APPROVED.
			_, err := r.validateTaskGoalDispatch(ctx, taskID)
			if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, model.ErrGoalConflict) {
				t.Fatalf("task validation: allowed=%v err=%v", tc.allowed, err)
			}
			release, err := r.beginTaskWorkerLaunch(ctx, taskID)
			if release != nil {
				release()
			}
			if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, model.ErrGoalConflict) {
				t.Fatalf("launch admission: allowed=%v err=%v", tc.allowed, err)
			}
			_, settle, err := r.superviseTask(ctx, taskID)
			if settle != nil {
				if settleErr := settle(); settleErr != nil {
					t.Fatal(settleErr)
				}
			}
			if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, model.ErrGoalConflict) {
				t.Fatalf("supervision: allowed=%v err=%v", tc.allowed, err)
			}
		})
	}
}

func TestCancelledGoalRejectsWorkerEntryPoints(t *testing.T) {
	for _, entry := range []string{"supervision", "claim", "run"} {
		t.Run(entry, func(t *testing.T) {
			r, ctx, e, _ := goalCancelFixture(t, model.ConfirmationApproved)
			taskEnvelope := e
			taskEnvelope.TargetID = "TASK-cancelled-goal"
			taskEnvelope.ExpectedVersion = 0
			taskEnvelope.IdempotencyKey = "create-bound-task"
			task, err := r.CommandTask(ctx, taskEnvelope, TaskCommand{Operation: "create", Title: "must not dispatch"})
			if err != nil {
				t.Fatal(err)
			}
			agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "goal-worker", Role: model.RoleDeveloper, ModelProvider: "codex"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = r.CommandCancelGoal(ctx, e, "stop"); err != nil {
				t.Fatal(err)
			}
			switch entry {
			case "supervision":
				_, settle, admissionErr := r.superviseTask(context.Background(), task.ID)
				if admissionErr == nil {
					_ = settle()
					t.Fatal("cancelled goal admitted to supervision")
				}
			case "claim":
				if _, err = r.Claim(ctx, ClaimRequest{TaskID: task.ID, AgentID: agent.ID, ExpectedRevision: task.Revision}); err == nil {
					t.Fatal("cancelled goal claimed")
				}
			case "run":
				_, err = r.Run(ctx, RunRequest{TaskID: task.ID, AgentID: agent.ID, Adapter: "codex", ExpectedRevision: task.Revision})
				// The goal must refuse before adapter discovery, worktree preparation or a lease.
				if err == nil {
					t.Fatal("cancelled goal ran")
				}
				count, countErr := r.store.Count(ctx, "sessions")
				if countErr != nil || count != 0 {
					t.Fatalf("worker created session after cancellation: %d %v (run=%v)", count, countErr, err)
				}
			}
		})
	}
}

type preparingGoalAdapter struct {
	*fakeGovernedCodexAdapter
	preparing chan struct{}
	proceed   chan struct{}
}

func (a *preparingGoalAdapter) Probe(ctx context.Context) (adapter.Probe, error) {
	close(a.preparing)
	select {
	case <-a.proceed:
		return a.fakeGovernedCodexAdapter.Probe(ctx)
	case <-ctx.Done():
		return adapter.Probe{}, ctx.Err()
	}
}

func TestWorkerLaunchRevalidatesGoalAfterProviderPreparation(t *testing.T) {
	r, ctx, e, _ := goalCancelFixture(t, model.ConfirmationApproved)
	a := &preparingGoalAdapter{fakeGovernedCodexAdapter: newFakeCodexAdapter(), preparing: make(chan struct{}), proceed: make(chan struct{})}
	r.adapters = map[string]adapter.Adapter{"codex": a}
	taskEnvelope := e
	taskEnvelope.TargetID = "TASK-preparing-goal"
	taskEnvelope.ExpectedVersion = 0
	taskEnvelope.IdempotencyKey = "create-preparing"
	task, err := r.CommandTask(ctx, taskEnvelope, TaskCommand{Operation: "create", Title: "must use current goal"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "preparing-worker", Role: model.RoleDeveloper, ModelProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if isExtremeBuild {
		// Extreme has no legacy unqualified worker route: the launch is refused
		// for missing mandatory project context before any provider
		// preparation, so a goal revision can never race a legacy launch. The
		// Process 05 goal barrier is covered by the harness admission and cell
		// admission tests.
		_, err := r.Run(ctx, RunRequest{TaskID: task.ID, AgentID: agent.ID, Adapter: "codex", ExpectedRevision: task.Revision})
		if !errors.Is(err, projectcontext.ErrReviewPending) {
			t.Fatalf("legacy Marshal worker route error = %v, want required-context review pending", err)
		}
		select {
		case <-a.preparing:
			t.Fatal("provider preparation started for a refused legacy launch")
		default:
		}
		if a.runs != 0 {
			t.Fatalf("provider launched %d times on a refused legacy route", a.runs)
		}
		if current, getErr := r.store.GetTask(ctx, task.ID); getErr != nil || current.Status != model.TaskReady || current.Worktree != nil {
			t.Fatalf("refused legacy launch mutated task: %+v %v", current, getErr)
		}
		return
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, RunRequest{TaskID: task.ID, AgentID: agent.ID, Adapter: "codex", ExpectedRevision: task.Revision})
		done <- err
	}()
	select {
	case <-a.preparing:
	case err := <-done:
		t.Fatalf("worker stopped before preparation: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("preparation did not start")
	}
	goal, err := r.store.GetActiveGoalContract(ctx, e.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	goal.Revision++
	goal.Confirmation = model.ConfirmationPending
	if err = r.store.SaveGoalContract(ctx, goal, e.ExpectedVersion); err != nil {
		t.Fatal(err)
	}
	close(a.proceed)
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("stale goal launched provider")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch did not settle")
	}
	if a.runs != 0 {
		t.Fatalf("provider launched %d times after goal revision changed", a.runs)
	}
}

func TestGoalCancellationSignalsSupervisedTask(t *testing.T) {
	r, ctx, e, _ := goalCancelFixture(t, model.ConfirmationApproved)
	taskEnvelope := e
	taskEnvelope.TargetID = "TASK-live-goal"
	taskEnvelope.ExpectedVersion = 0
	taskEnvelope.IdempotencyKey = "create-live"
	task, err := r.CommandTask(ctx, taskEnvelope, TaskCommand{Operation: "create", Title: "stop live worker"})
	if err != nil {
		t.Fatal(err)
	}
	turn, settle, err := r.superviseTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := settle(); err != nil {
			t.Error(err)
		}
	}()
	result, err := r.CommandCancelGoal(ctx, e, "stop")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Settling {
		t.Fatal("goal cancellation reported settled while supervised task was still live")
	}
	select {
	case <-turn.Done():
	case <-time.After(time.Second):
		t.Fatal("goal cancellation did not interrupt supervised task")
	}
}
