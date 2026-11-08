package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestTaskResumeRejectsDurableGoalTermination(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := context.Background()
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := local.Context(ctx)
	goal := planGoal()
	goal.ID = "GOAL-terminated-task"
	goal.SessionID = "SESSION-terminated-task"
	goal.ProjectID = r.ProjectIdentity()
	if err = r.store.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: goal.SessionID, TargetID: "TASK-terminated", IdempotencyKey: "create"}
	task, err := r.CommandTask(owner, e, TaskCommand{Operation: "create", Title: "no cancelled dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "pause"
	task, err = r.CommandTask(owner, e, TaskCommand{Operation: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.store.SaveGoalTermination(ctx, model.GoalTermination{SessionID: goal.SessionID, GoalID: goal.ID, GoalRevision: goal.Revision, State: model.StateCancelled, ReasonCode: model.ReasonUserCancelled, CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "resume"
	if _, err = r.CommandTask(owner, e, TaskCommand{Operation: "resume"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("cancelled Goal resume=%v", err)
	}
	read, err := r.Task(ctx, task.ID)
	if err != nil || read.Revision != task.Revision || read.ControlState != "paused" {
		t.Fatalf("resume changed state: %+v %v", read, err)
	}
}
