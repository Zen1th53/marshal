package execution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestM04DefaultDeliveryExistingProcess05(t *testing.T) {
	if DeliveryReconcile != "" {
		t.Fatal("default delivery changed")
	}
}

func TestM04DefaultDeliveryReleasesLease(t *testing.T) {
	root := t.TempDir()
	engine, err := NewEngine(EngineConfig{ProjectRoot: root}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	goal, p := createTestGoalAndPlan(time.Now().UTC())
	run, err := engine.InitializeRun(context.Background(), createTestHandoff(t, root, goal, p), goal, p)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := engine.ExecuteRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range completed.Tasks {
		if task.Mutates {
			lease := engine.leases.leases[task.LeaseID]
			if lease == nil || lease.Status != LeaseReleased {
				t.Fatalf("default lease retained: %#v", lease)
			}
		}
	}
}

func TestM04PreserveBranchCommitRootBaseResultAndLease(t *testing.T) {
	repo := testgit.New(t)
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
	goal, p := createTestGoalAndPlan(time.Now().UTC())
	h := createTestHandoff(t, repo.Path(), goal, p)
	run, err := engine.InitializeRun(ctx, h, goal, p)
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
	if repo.HEAD(t) != base {
		t.Fatal("project root HEAD changed")
	}
	for _, task := range completed.Tasks {
		if task.BaseCommit != base || task.ResultCommit == "" || task.ResultCommit == base {
			t.Fatalf("commits for %s: %s %s", task.TaskID, task.BaseCommit, task.ResultCommit)
		}
		if _, err := os.Stat(task.WorktreePath); err != nil {
			t.Fatalf("worktree removed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(repo.Path(), task.TaskID+".txt")); !os.IsNotExist(err) {
			t.Fatalf("task file copied into project root: %v", err)
		}
		cmd := exec.Command("git", "-C", task.WorktreePath, "rev-parse", "HEAD")
		out, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(out)) != task.ResultCommit {
			t.Fatalf("branch commit mismatch: %s %v", out, err)
		}
		if task.Mutates {
			lease := engine.leases.leases[task.LeaseID]
			if lease == nil || lease.Status != LeaseReleased {
				t.Fatalf("lease retained: %#v", lease)
			}
		}
		relaunched, err := engine.prepareTaskWorktree(ctx, *completed, task)
		if err != nil || relaunched != task.WorktreePath {
			t.Fatalf("rework path = %q, %v", relaunched, err)
		}
		if err := os.WriteFile(filepath.Join(relaunched, task.TaskID+"-rework.txt"), []byte("rework"), 0o600); err != nil {
			t.Fatal(err)
		}
		second, err := commitTaskWorktree(ctx, relaunched, completed.RunID, task.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.BaseCommit != base {
			t.Fatalf("rework changed base: %s", task.BaseCommit)
		}
		cmd = exec.Command("git", "-C", relaunched, "diff", "--name-only", base, second)
		out, err = cmd.Output()
		if err != nil || !strings.Contains(string(out), task.TaskID+".txt") || !strings.Contains(string(out), task.TaskID+"-rework.txt") {
			t.Fatalf("rework diff lost changes: %s %v", out, err)
		}
	}
}

func TestM04PreserveBranchCommitUsesConfiguredIdentityAndTaskMessage(t *testing.T) {
	repo := testgit.New(t)
	config := filepath.Join(t.TempDir(), "empty-config")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_SYSTEM", config)
	for _, args := range [][]string{{"config", "--local", "user.name", "Task Author"}, {"config", "--local", "user.email", "task@example.invalid"}} {
		cmd := exec.Command("git", append([]string{"-C", repo.Path()}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("configure identity: %s %v", out, err)
		}
	}
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo.Path(), name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("first.txt")
	if _, err := commitTaskWorktree(context.Background(), repo.Path(), "run-1", "task-2"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo.Path(), "show", "-s", "--format=%an <%ae>%n%s", "HEAD")
	out, err := cmd.Output()
	if err != nil || string(out) != "Task Author <task@example.invalid>\nmarshal: hand-in for task-2 (run run-1)\n" {
		t.Fatalf("commit identity/message: %q %v", out, err)
	}
	for _, key := range []string{"user.name", "user.email"} {
		cmd = exec.Command("git", "-C", repo.Path(), "config", "--local", "--unset-all", key)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("unset %s: %s %v", key, out, err)
		}
	}
	write("second.txt")
	if _, err := commitTaskWorktree(context.Background(), repo.Path(), "run-1", "task-2"); err == nil || !strings.Contains(err.Error(), "user.name") {
		t.Fatalf("missing identity error: %v", err)
	}
}

func TestM04PreserveBranchNeverCopiesOnGitFailure(t *testing.T) {
	root := t.TempDir()
	engine, err := NewEngine(EngineConfig{ProjectRoot: root}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.prepareTaskWorktree(context.Background(), ExecutionRun{Delivery: DeliveryPreserveBranch, RunID: "run", BaseCommit: "missing"}, TaskExecution{TaskID: "task"})
	if err == nil {
		t.Fatal("non-git project accepted branch delivery")
	}
	if _, err := os.Stat(filepath.Join(root, ".marshal", "branches", "TASK-task")); !os.IsNotExist(err) {
		t.Fatal("directory-copy fallback created")
	}
}
