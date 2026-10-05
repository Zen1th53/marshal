package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestCodexPreparationFailureReleasesClaimAndSession(t *testing.T) {
	repo := runtimeRepo(t)
	ctx := t.Context()
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	fake := newFakeCodexAdapter()
	rt, err := OpenWithOptions(ctx, repo.Path(), Options{Adapters: map[string]adapter.Adapter{"codex": fake}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	agent, err := rt.RegisterAgent(ctx, RegisterAgentRequest{Name: "Codex", Role: model.RoleDeveloper, ModelProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	missing := "missing-base-commit"
	if _, err := rt.ImportTasks(ctx, []model.Task{{ID: "TASK-fix6-preparation", Title: "write output", Status: model.TaskReady, Risk: model.R1, BaseCommit: &missing}}); err != nil {
		t.Fatal(err)
	}
	claim, err := rt.Claim(ctx, ClaimRequest{TaskID: "TASK-fix6-preparation", AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Run(ctx, RunRequest{TaskID: "TASK-fix6-preparation", AgentID: agent.ID, Adapter: "codex", Model: "gpt-5.6-terra", ExpectedRevision: 1}); err == nil {
		t.Fatal("missing base accepted")
	}
	if fake.runs != 0 {
		t.Fatal("worker launched without a valid base")
	}
	if _, err := rt.Store().ActiveLease(ctx, "TASK-fix6-preparation"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("preparation failure retained lease: %v", err)
	}
	session, err := rt.Store().GetSession(ctx, claim.Session.ID)
	if err != nil || session.Status != model.SessionTerminated || session.TaskID != nil {
		t.Fatalf("preparation failure retained session: %+v %v", session, err)
	}
	task, err := rt.Task(ctx, "TASK-fix6-preparation")
	if err != nil || task.Status != model.TaskReady || task.OwnerAgentID != nil {
		t.Fatalf("preparation failure prevents reassignment: %+v %v", task, err)
	}
	if _, err := rt.Claim(ctx, ClaimRequest{TaskID: task.ID, AgentID: agent.ID, ExpectedRevision: task.Revision}); err != nil {
		t.Fatalf("cannot reclaim after preparation failure: %v", err)
	}
}
