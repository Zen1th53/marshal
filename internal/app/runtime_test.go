package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/slaves/internal/model"
	"github.com/Zen1th53/slaves/internal/testutil/testgit"
)

func TestBootstrapIsIdempotentAndDoesNotInventTasks(t *testing.T) {
	repo := runtimeRepo(t)
	for i := 0; i < 2; i++ {
		if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
			t.Fatalf("Bootstrap %d: %v", i+1, err)
		}
	}
	runtime, err := Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	status, err := runtime.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.SchemaVersion != 1 || status.TaskCount != 0 || status.Project.Repository != repo.Path() {
		t.Fatalf("status = %#v", status)
	}
	info, err := os.Stat(filepath.Join(repo.Path(), ".slaves"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime mode = %o", info.Mode().Perm())
	}
}

func TestRuntimeAgentImportClaimReleaseFlow(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	agent, err := runtime.RegisterAgent(context.Background(), RegisterAgentRequest{
		Name: "local-codex", Role: model.RoleDeveloper,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.ImportTasks(context.Background(), []model.Task{{
		ID: "TASK-001", Title: "runtime", Status: model.TaskReady, Risk: model.R1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 {
		t.Fatalf("import = %#v", result)
	}
	tasks, err := runtime.Tasks(context.Background())
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%#v err=%v", tasks, err)
	}
	claim, err := runtime.Claim(context.Background(), ClaimRequest{
		TaskID: "TASK-001", AgentID: agent.ID, ExpectedRevision: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.Lease.TaskID != "TASK-001" || claim.Session.AgentID != agent.ID {
		t.Fatalf("claim = %#v", claim)
	}
	if err := runtime.Release(context.Background(), ReleaseRequest{TaskID: "TASK-001"}); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.Task(context.Background(), "TASK-001")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskReady || task.Revision != 2 {
		t.Fatalf("released task = %#v", task)
	}
}

func runtimeRepo(t *testing.T) *testgit.Repository {
	t.Helper()
	repo := testgit.New(t)
	sourceRoot := filepath.Join("..", "..")
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join(sourceRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}
