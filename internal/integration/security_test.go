package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestSecurityPolicyUnavailableFailsClosedBeforeMutation(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(repo.Path(), "CAPABILITIES.yaml")
	original, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Open(context.Background(), repo.Path()); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("open error = %v", err)
	}
	if err := os.WriteFile(policyPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	status, err := runtime.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.AgentCount != 0 || status.TaskCount != 0 {
		t.Fatalf("mutation occurred while policy unavailable: %#v", status)
	}
}

func TestSecurityWorkerCrashPreservesWorktreeAndNeverCompletes(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	layout, err := app.Bootstrap(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := app.OpenWithOptions(context.Background(), repo.Path(), app.Options{Adapters: map[string]adapter.Adapter{"crash": crashAdapter{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	agent, err := runtime.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "crash", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{ID: "TASK-CRASH", Title: "crash", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), app.RunRequest{TaskID: "TASK-CRASH", AgentID: agent.ID, Adapter: "crash"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" {
		t.Fatalf("result = %#v", result)
	}
	if result.StdoutArtifact.ID == "" || result.StdoutArtifact.ID != result.StderrArtifact.ID {
		t.Fatalf("identical streams were not deduplicated: %#v", result)
	}
	task, err := runtime.Task(context.Background(), "TASK-CRASH")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskBlocked || task.Status == model.TaskReview || task.Status == model.TaskMerged {
		t.Fatalf("task = %#v", task)
	}
	if _, err := os.Stat(filepath.Join(layout.Worktrees, "TASK-CRASH")); err != nil {
		t.Fatalf("worktree not preserved: %v", err)
	}
}

type crashAdapter struct{}

func (crashAdapter) Probe(context.Context) (adapter.Probe, error) {
	return adapter.Probe{Name: "crash", Available: true, Version: "test"}, nil
}
func (crashAdapter) Run(context.Context, adapter.Request) (adapter.Result, error) {
	now := time.Now().UTC()
	return adapter.Result{Adapter: "crash", AdapterVersion: "test", Status: adapter.StatusFailure, ExitCode: 137, StartedAt: now, EndedAt: now, Stdout: []byte("worker exited"), Stderr: []byte("worker exited")}, nil
}
func (crashAdapter) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusFailure, nil
}
func (crashAdapter) Resume(context.Context, string, adapter.Request) (adapter.Result, error) {
	return adapter.Result{}, model.ErrUnavailable
}
func (crashAdapter) Capabilities() map[string]string               { return map[string]string{"run": "native"} }
func (crashAdapter) CollectEvidence(adapter.Result) map[string]any { return map[string]any{} }
func (crashAdapter) Shutdown(context.Context, string) error        { return nil }
