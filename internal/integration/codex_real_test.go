package integration

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/Zen1th53/slaves/internal/adapter"
	"github.com/Zen1th53/slaves/internal/adapter/codex"
	"github.com/Zen1th53/slaves/internal/app"
	"github.com/Zen1th53/slaves/internal/model"
	"github.com/Zen1th53/slaves/internal/testutil/testgit"
	"github.com/Zen1th53/slaves/internal/worker"
)

func TestRealCodexAdapter(t *testing.T) {
	if os.Getenv("SLAVES_TEST_REAL_CODEX") != "1" {
		t.Skip("set SLAVES_TEST_REAL_CODEX=1 for authenticated external integration")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	repo := testgit.New(t)
	runner := worker.New(3*time.Minute, 2*time.Second, 8<<20)
	client := codex.New(binary, runner)
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID: "TASK-REAL", Title: "Create runtime-proof.txt containing exactly SLAVES runtime proof, then commit it.",
		Worktree: repo.Path(), BaseCommit: repo.HEAD(t), HeadCommit: repo.HEAD(t),
		AllowedOperations: []string{"filesystem.write", "shell.execute", "git.commit"},
		EvidenceRequired:  []string{"git status --short", "git log -1 --oneline"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != adapter.StatusSuccess {
		t.Fatalf("result = %#v", result)
	}
}

func TestRealCodexRuntime(t *testing.T) {
	if os.Getenv("SLAVES_TEST_REAL_CODEX") != "1" {
		t.Skip("set SLAVES_TEST_REAL_CODEX=1 for authenticated external integration")
	}
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	agent, err := runtime.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "real-codex", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{
		ID: "TASK-REAL-RUNTIME", Title: "Create runtime-real-proof.txt containing exactly SLAVES real runtime proof followed by a newline. Do not commit; the runtime commits changes.",
		Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), app.RunRequest{TaskID: "TASK-REAL-RUNTIME", AgentID: agent.ID, Adapter: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.ResultCommit == result.BaseCommit || result.Isolation.Level != model.IsolationBwrap {
		t.Fatalf("result = %#v", result)
	}
	task, err := runtime.Task(context.Background(), "TASK-REAL-RUNTIME")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskReview {
		t.Fatalf("task = %#v", task)
	}
}
