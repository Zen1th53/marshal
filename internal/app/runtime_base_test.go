package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestRuntimeRunsResolveLiveTargetBranchBase(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, repo.Path(), "switch", "-c", "operator-work")
	rt, err := OpenWithOptions(ctx, repo.Path(), Options{Adapters: map[string]adapter.Adapter{"codex": &baseMovingAdapter{newFakeCodexAdapter()}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	agent, err := rt.RegisterAgent(ctx, RegisterAgentRequest{Name: "worker", Role: model.RoleDeveloper, ModelProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	initial := repo.HEAD(t)
	for _, id := range []string{"TASK-first", "TASK-second", "TASK-explicit"} {
		task := model.Task{ID: id, Title: "write output", Status: model.TaskReady, Risk: model.R1}
		if id == "TASK-explicit" {
			task.BaseCommit = &initial
		}
		if _, err := rt.ImportTasks(ctx, []model.Task{task}); err != nil {
			t.Fatal(err)
		}
		want := marshalGit(t, repo.Path(), "rev-parse", "refs/heads/main")
		if id == "TASK-explicit" {
			want = initial
		}
		result, err := rt.Run(ctx, RunRequest{TaskID: id, AgentID: agent.ID, Adapter: "codex"})
		if err != nil {
			t.Fatal(err)
		}
		task, err = rt.Task(ctx, id)
		if err != nil || result.BaseCommit != want || task.BaseCommit == nil || *task.BaseCommit != want {
			t.Fatalf("%s: result base=%s task base=%v want=%s err=%v", id, result.BaseCommit, task.BaseCommit, want, err)
		}
		if id == "TASK-first" {
			marshalGit(t, repo.Path(), "switch", "main")
			marshalGit(t, repo.Path(), "commit", "--allow-empty", "-m", "advance target")
			marshalGit(t, repo.Path(), "switch", "operator-work")
		}
	}
}

// Distinct evidence per run avoids the store's global artifact digest deduplication.
type baseMovingAdapter struct{ *fakeGovernedCodexAdapter }

func (a *baseMovingAdapter) Run(ctx context.Context, req adapter.Request) (adapter.Result, error) {
	result, err := a.fakeGovernedCodexAdapter.Run(ctx, req)
	result.Stdout = []byte(fmt.Sprintf("run %d", a.runs))
	result.Stderr = []byte(fmt.Sprintf("stderr run %d", a.runs))
	return result, err
}
