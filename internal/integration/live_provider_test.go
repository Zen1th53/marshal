package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

// Called only by explicitly opted-in live tests in disposable repositories.
// Use the canonical decision surface so the broker grant has durable evidence.
func allowRealProviderCredentials(t *testing.T, rt *app.Runtime, provider string) {
	t.Helper()
	control, err := rt.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.CommandPermission(control.Context(context.Background()), permission.Request{
		Kind: "credential", Object: provider,
	}, true, "explicitly opted-in live provider qualification in disposable test repository"); err != nil {
		t.Fatal(err)
	}
}

func requireRealProviderProof(t *testing.T, rt *app.Runtime, result app.RunResult, filename, expected string) {
	t.Helper()
	if result.Status != "success" || result.ExitStatus != 0 || result.ResultCommit == "" || result.ResultCommit == result.BaseCommit || result.Isolation.Level != model.IsolationBwrap {
		stdout, _ := os.ReadFile(result.StdoutArtifact.Path)
		stderr, _ := os.ReadFile(result.StderrArtifact.Path)
		t.Fatalf("worker status=%s exit=%d isolation=%s\nstdout: %s\nstderr: %s", result.Status, result.ExitStatus, result.Isolation.Level, stdout, stderr)
	}
	proof, err := exec.Command("git", "-C", rt.ProjectRoot(), "show", result.ResultCommit+":"+filepath.ToSlash(filename)).Output()
	if err != nil {
		t.Fatalf("read committed provider proof: %v", err)
	}
	if string(proof) != expected {
		t.Fatalf("provider proof = %q, want %q", proof, expected)
	}
	task, err := rt.Task(context.Background(), result.TaskID)
	if err != nil || task.Status != model.TaskReview {
		t.Fatalf("task status=%s err=%v", task.Status, err)
	}
}

func TestRealClaudeRuntimeE2E(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_CLAUDE=1 for authenticated external integration")
	}
	ctx := context.Background()
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	allowRealProviderCredentials(t, rt, "claude")
	agent, err := rt.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "real-claude", Role: model.RoleDeveloper, ModelProvider: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(ctx, []model.Task{{
		ID: "TASK-REAL-CLAUDE-RUNTIME", Title: "Create claude-real-proof.txt containing exactly MARSHAL real Claude runtime proof followed by a newline. Do not commit; the runtime commits changes.",
		Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatal(err)
	}
	result, err := rt.Run(ctx, app.RunRequest{TaskID: "TASK-REAL-CLAUDE-RUNTIME", AgentID: agent.ID, Adapter: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	requireRealProviderProof(t, rt, result, "claude-real-proof.txt", "MARSHAL real Claude runtime proof\n")
}
