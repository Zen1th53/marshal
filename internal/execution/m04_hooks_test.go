package execution

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestM04PreserveBranchCommitDisablesRepositoryHooks(t *testing.T) {
	repo := testgit.New(t)
	hooks := t.TempDir()
	markers := t.TempDir()
	for _, name := range []string{"pre-commit", "prepare-commit-msg", "commit-msg", "post-commit"} {
		marker := filepath.Join(markers, name)
		script := fmt.Sprintf("#!/bin/sh\nprintf ran > %q\n", marker)
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("git", "-C", repo.Path(), "config", "--local", "core.hooksPath", hooks)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("configure hooks: %s %v", out, err)
	}

	ctx := context.Background()
	engine, err := NewEngine(EngineConfig{ProjectRoot: repo.Path()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	engine.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, path string) (TaskResult, error) {
		if err := os.WriteFile(filepath.Join(path, task.TaskID+".txt"), []byte("change"), 0o600); err != nil {
			return TaskResult{}, err
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	goal, plan := createTestGoalAndPlan(time.Now().UTC())
	run, err := engine.InitializeRun(ctx, createTestHandoff(t, repo.Path(), goal, plan), goal, plan)
	if err != nil {
		t.Fatal(err)
	}
	base := repo.HEAD(t)
	if err := engine.SetPreserveBranch(ctx, run.RunID, base); err != nil {
		t.Fatal(err)
	}
	completed, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range completed.Tasks {
		if task.State != TaskCompletedPendingVerify || task.ResultCommit == "" || task.ResultCommit == base {
			t.Fatalf("task result or commit missing: %#v", task)
		}
		cmd := exec.Command("git", "-C", task.WorktreePath, "rev-parse", "HEAD")
		if out, err := cmd.Output(); err != nil || string(out) != task.ResultCommit+"\n" {
			t.Fatalf("result commit not recorded: %q %v", out, err)
		}
	}
	for _, name := range []string{"pre-commit", "prepare-commit-msg", "commit-msg", "post-commit"} {
		if _, err := os.Stat(filepath.Join(markers, name)); !os.IsNotExist(err) {
			t.Fatalf("%s hook ran: %v", name, err)
		}
	}
}
