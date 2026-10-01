package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestCommandRunControlPausesResumesAndCancels(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	store, err := execution.NewFileRunStore(filepath.Join(repo.Path(), ".marshal", "execution", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	binding, found := projectid.LoadBinding(filepath.Join(repo.Path(), projectid.StateDirName))
	if !found {
		t.Fatal("no project binding")
	}
	for _, run := range []execution.ExecutionRun{
		{RunID: "RUN-ctl", ProjectID: binding.ID, SessionID: "S", GoalID: "GOAL-ctl", GoalRevision: 1, State: execution.RunRunning, StartedAt: now, UpdatedAt: now},
		{RunID: "RUN-other", ProjectID: binding.ID, SessionID: "OTHER", GoalID: "GOAL-x", GoalRevision: 1, State: execution.RunRunning, StartedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	goal := model.GoalContract{ID: "GOAL-ctl", SessionID: "S", ProjectID: r.ProjectIdentity(), Revision: 1, OriginalRequest: "x", DesiredOutcome: "x",
		ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationApproved, Risk: model.R1, AuthoritySource: "owner"}
	if err := r.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	restarted := make(chan string, 2)
	r.resumeRun = func(id string) { restarted <- id }
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := local.Context(ctx)
	run, _ := r.Execution().Engine().GetRun(ctx, "RUN-ctl")
	env := func(key string, version int64) CommandEnvelope {
		return CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "S", TargetID: "run:RUN-ctl", ExpectedVersion: version, IdempotencyKey: key}
	}

	if _, err := r.CommandRunControl(ctx, env("p0", run.Version), "pause"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous: %v", err)
	}
	paused, err := r.CommandRunControl(owner, env("p1", run.Version), "pause")
	if err != nil || paused.Run.State != execution.RunPaused || paused.Executing {
		t.Fatalf("pause: %+v %v", paused, err)
	}
	if replay, err := r.CommandRunControl(owner, env("p1", run.Version), "pause"); err != nil || replay.Run.State != execution.RunPaused {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if _, err := r.CommandRunControl(owner, env("p2", run.Version), "resume"); !errors.Is(err, execution.ErrRunConflict) {
		t.Fatalf("stale version: %v", err)
	}
	resumed, err := r.CommandRunControl(owner, env("r1", paused.Run.Version), "resume")
	if err != nil || resumed.Run.State != execution.RunRunning {
		t.Fatalf("resume: %+v %v", resumed, err)
	}
	if id := <-restarted; id != "RUN-ctl" {
		t.Fatalf("resume restarted %q", id)
	}

	// A paused run whose goal has since been revised cannot resume.
	paused, err = r.CommandRunControl(owner, env("p3", resumed.Run.Version), "pause")
	if err != nil {
		t.Fatal(err)
	}
	goal.Revision, goal.DesiredOutcome = 2, "changed"
	if err := r.Store().SaveGoalContract(ctx, goal, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommandRunControl(owner, env("r2", paused.Run.Version), "resume"); !errors.Is(err, execution.ErrRunBlocked) {
		t.Fatalf("resume against a superseded goal: %v", err)
	}

	cancelled, err := r.CommandRunControl(owner, env("c1", paused.Run.Version), "cancel")
	if err != nil || cancelled.Run.State != execution.RunCancelled {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	if _, err := r.CommandRunControl(owner, env("c2", cancelled.Run.Version), "cancel"); !errors.Is(err, execution.ErrInvalidStateTransition) {
		t.Fatalf("cancel twice: %v", err)
	}
	other := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "S", TargetID: "run:RUN-other", ExpectedVersion: 0, IdempotencyKey: "x"}
	if _, err := r.CommandRunControl(owner, other, "pause"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("another session's run: %v", err)
	}
}
