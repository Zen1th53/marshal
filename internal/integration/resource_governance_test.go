package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/worker"
)

type hangingAdapter struct {
	fakeCommitAdapter
}

func (hangingAdapter) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	process := worker.New(100*time.Millisecond, 50*time.Millisecond, 1<<20)
	res, err := process.Run(ctx, adapter.Command{
		Path: "/bin/sh",
		Args: []string{"-c", "sleep 30"},
		Dir:  request.Worktree,
	})
	return adapter.Result{
		Status:    adapter.StatusFailure,
		TimedOut:  res.TimedOut,
		ExitCode:  res.ExitCode,
		StartedAt: res.StartedAt,
		EndedAt:   res.EndedAt,
	}, err
}

func TestExecutionTimeoutTerminatesWorkerCleanly(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}

	rt, err := app.OpenWithOptions(context.Background(), repo.Path(), app.Options{
		Adapters: map[string]adapter.Adapter{
			"hanging": hangingAdapter{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	agent, err := rt.RegisterAgent(context.Background(), app.RegisterAgentRequest{
		Name: "timeout-worker",
		Role: model.RoleDeveloper,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.ImportTasks(context.Background(), []model.Task{
		{ID: "TASK-TIMEOUT-001", Title: "Test timeout worker termination", Status: model.TaskReady, Risk: model.R1},
	}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	res, err := rt.Run(context.Background(), app.RunRequest{
		TaskID:  "TASK-TIMEOUT-001",
		AgentID: agent.ID,
		Adapter: "hanging",
	})
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Fatalf("execution took too long to terminate: %v", elapsed)
	}
	if res.Status == "success" {
		t.Fatalf("expected failed or timeout status, got: %s", res.Status)
	}
}

type oversizedOutputAdapter struct {
	fakeCommitAdapter
}

func (oversizedOutputAdapter) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	process := worker.New(2*time.Second, 100*time.Millisecond, 1024)
	res, err := process.Run(ctx, adapter.Command{
		Path: "/bin/sh",
		Args: []string{"-c", "head -c 5000 /dev/zero | tr '\\0' 'a'"},
		Dir:  request.Worktree,
	})
	return adapter.Result{
		Status:          adapter.StatusSuccess,
		ExitCode:        res.ExitCode,
		Stdout:          res.Stdout,
		Stderr:          res.Stderr,
		OutputTruncated: res.OutputTruncated,
		StartedAt:       res.StartedAt,
		EndedAt:         res.EndedAt,
	}, err
}

func TestOutputLimitTruncationEnforced(t *testing.T) {
	adapterInstance := oversizedOutputAdapter{}
	worktree := t.TempDir()
	res, err := adapterInstance.Run(context.Background(), adapter.Request{
		Worktree: worktree,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OutputTruncated {
		t.Fatal("expected OutputTruncated to be true for output exceeding 1024 bytes")
	}
	if len(res.Stdout) != 1024 {
		t.Fatalf("expected stdout length 1024, got %d", len(res.Stdout))
	}
}

type hugeDiskAdapter struct {
	fakeCommitAdapter
}

func (hugeDiskAdapter) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	res, err := (fakeCommitAdapter{}).Run(ctx, request)
	if err != nil {
		return res, err
	}
	// Write a file to worktree
	hugeFile := filepath.Join(request.Worktree, "huge_data.bin")
	_ = os.WriteFile(hugeFile, make([]byte, 1024), 0o600)
	return res, nil
}

func TestWorktreeDirectorySizeCalculation(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "f1.txt")
	file2 := filepath.Join(dir, "f2.txt")
	_ = os.WriteFile(file1, []byte("hello"), 0o600)
	_ = os.WriteFile(file2, []byte("world!"), 0o600)

	total, err := app.CalculateDirectorySize(dir)
	if err != nil {
		t.Fatal(err)
	}
	if total != 11 {
		t.Fatalf("expected total size 11, got %d", total)
	}
}
