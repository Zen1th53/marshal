package app

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/app/control"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
)

type unknownControlOperation struct{ control.RunPause }

func (unknownControlOperation) Name() string { return "unknown" }

func TestControlAdapterParity(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fs, err := execution.NewFileRunStore(filepath.Join(repo.Path(), statepath.DirName, "execution", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	run := execution.ExecutionRun{RunID: "run-control", ProjectID: r.admittedProjectIdentity, SessionID: "session", GoalID: "goal-control", GoalRevision: 1, State: execution.RunRunning, Tasks: map[string]execution.TaskExecution{"t": {TaskID: "t", Dependencies: []string{"a"}}}}
	if err := fs.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	a := NewControlAdapter(r)
	direct, err := r.Execution().GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := a.Snapshot(ctx, control.Run{ID: run.RunID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap.Runs, []control.RunDTO{copyControlRun(direct)}) {
		t.Fatalf("%+v", snap)
	}
	snap.Runs[0].Tasks[0].Dependencies[0] = "changed"
	again, _ := a.Snapshot(ctx, control.Run{ID: run.RunID})
	if again.Runs[0].Tasks[0].Dependencies[0] != "a" {
		t.Fatal("aliased DTO")
	}
	goal := model.GoalContract{ID: "goal-control", SessionID: "session", ProjectID: r.ProjectIdentity(), Revision: 1, OriginalRequest: "x", DesiredOutcome: "x", ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationApproved, Risk: model.R1, AuthoritySource: "owner"}
	if err := r.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := local.Context(ctx)
	c := control.Command{RequestID: "pause", ProjectID: r.ProjectIdentity(), SessionID: "session", TargetID: "run:" + run.RunID, ExpectedRevision: direct.Version, IdempotencyKey: "pause", Operation: control.RunPause{}}
	got, err := a.Submit(owner, c)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := r.CommandRunControl(owner, CommandEnvelope{ProjectID: c.ProjectID, SessionID: c.SessionID, TargetID: c.TargetID, ExpectedVersion: c.ExpectedRevision, IdempotencyKey: c.IdempotencyKey}, "pause")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != replay.Run.Version || replay.Run.State != execution.RunPaused {
		t.Fatalf("%+v %+v", got, replay)
	}
	receipt, err := a.Receipt(ctx, c.RequestID)
	if err != nil || receipt != got {
		t.Fatalf("%+v %v", receipt, err)
	}
	r.resumeRun = func(string) {}
	c.RequestID = "resume"
	c.IdempotencyKey = "resume"
	c.ExpectedRevision = got.Revision
	c.Operation = control.RunResume{}
	resumed, err := a.Submit(owner, c)
	if err != nil {
		t.Fatal(err)
	}
	directResume, err := r.CommandRunControl(owner, CommandEnvelope{ProjectID: c.ProjectID, SessionID: c.SessionID, TargetID: c.TargetID, ExpectedVersion: c.ExpectedRevision, IdempotencyKey: c.IdempotencyKey}, "resume")
	if err != nil || resumed.Revision != directResume.Run.Version {
		t.Fatalf("resume: %+v %v", resumed, err)
	}
	task := model.Task{ID: "TASK-retry-control", Title: "retry", Status: model.TaskBlocked, Risk: model.R1}
	if _, err := r.ImportTasks(ctx, []model.Task{task}); err != nil {
		t.Fatal(err)
	}
	task, err = r.Store().GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.RequestID = "retry"
	c.IdempotencyKey = "retry"
	c.TargetID = task.ID
	c.ExpectedRevision = task.Revision
	c.Operation = control.TaskRetry{}
	retried, err := a.Submit(owner, c)
	if err != nil {
		t.Fatal(err)
	}
	directRetry, err := r.CommandTask(owner, CommandEnvelope{ProjectID: c.ProjectID, SessionID: c.SessionID, TargetID: c.TargetID, ExpectedVersion: c.ExpectedRevision, IdempotencyKey: c.IdempotencyKey}, TaskCommand{Operation: "retry"})
	if err != nil || retried.Revision != directRetry.Revision {
		t.Fatalf("retry: %+v %v", retried, err)
	}
	for _, op := range []control.Operation{nil, (*control.RunPause)(nil), unknownControlOperation{}} {
		c.RequestID = "invalid"
		c.Operation = op
		if _, err := a.Submit(owner, c); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("unknown operation: %v", err)
		}
	}
}
