package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/risk"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
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
	if status.SchemaVersion != store.LatestSchemaVersion || status.TaskCount != 0 || status.Project.Repository != repo.Path() {
		t.Fatalf("status = %#v", status)
	}
	info, err := os.Stat(filepath.Join(repo.Path(), ".marshal"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime mode = %o", info.Mode().Perm())
	}
}

func TestRuntimeAssessesToolThroughCanonicalRiskService(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	assessment, err := runtime.AssessTool(context.Background(), risk.AssessmentRequest{
		ID: "assessment-a06",
		Descriptor: risk.ToolDescriptor{
			Tool: "git", Action: "push", Resource: "repo:marshal",
			Factors: risk.Factors{ExternalWrite: true},
		},
	})
	if err != nil {
		t.Fatalf("AssessTool: %v", err)
	}
	if assessment.Level != risk.LevelHigh || assessment.State != risk.StateRequirementsEmitted {
		t.Fatalf("assessment=%+v", assessment)
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

func TestRuntimeInstanceIDAndCancellation(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	if runtime.InstanceID() == "" {
		t.Fatal("expected non-empty instance ID")
	}
	agent, err := runtime.RegisterAgent(context.Background(), RegisterAgentRequest{
		Name: "cancel-test-agent", Role: model.RoleDeveloper,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.ImportTasks(context.Background(), []model.Task{{
		ID: "TASK-CANCEL-001", Title: "cancelable task", Status: model.TaskReady, Risk: model.R1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Claim(context.Background(), ClaimRequest{
		TaskID: "TASK-CANCEL-001", AgentID: agent.ID, ExpectedRevision: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CancelTask(context.Background(), "TASK-CANCEL-001"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	task, err := runtime.Task(context.Background(), "TASK-CANCEL-001")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskBlocked && task.Status != model.TaskCancelled {
		t.Fatalf("canceled task status = %s, want %s or %s", task.Status, model.TaskBlocked, model.TaskCancelled)
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
