package execution

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

// A worker that succeeded leaves work the preserve-branch commit cannot
// record (Git has no identity, as on the macOS demo machine). The run's
// error names the cause instead of only "one or more tasks failed", and is
// marked as a failure after the worker finished so callers do not re-run
// the worker for it.
func TestPreserveBranchHandInFailureNamesCauseAndIsMarked(t *testing.T) {
	repo := testgit.New(t)
	for _, key := range []string{"user.name", "user.email"} {
		cmd := exec.Command("git", "-C", repo.Path(), "config", "--local", "--unset-all", key)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("unset %s: %s %v", key, out, err)
		}
	}
	empty := filepath.Join(t.TempDir(), "empty-config")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_SYSTEM", empty)
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
	run, err := engine.InitializeRun(ctx, createTestHandoff(t, repo.Path(), goal, p), goal, p)
	if err != nil {
		t.Fatal(err)
	}
	base := repo.HEAD(t)
	if err := engine.SetPreserveBranch(ctx, run.RunID, base); err != nil {
		t.Fatal(err)
	}
	completed, err := engine.ExecuteTaskBound(ctx, run.RunID, "task-1", base)
	if err == nil {
		t.Fatal("a hand-in Git could not commit was accepted")
	}
	if !strings.Contains(err.Error(), "run failed: one or more tasks failed: ") || !strings.Contains(err.Error(), "user.name") {
		t.Fatalf("run error = %q, want the hand-in cause", err)
	}
	if !errors.Is(err, ErrHandInNotRecorded) {
		t.Fatalf("run error %v is not marked as a failure after the worker finished", err)
	}
	task := completed.Tasks["task-1"]
	found := false
	for _, f := range completed.Failures {
		if f.TaskID == task.TaskID && f.Stage == "HAND_IN" && f.Reason == task.LastFailureReason {
			found = true
		}
		if f.Stage == "EXECUTE" {
			t.Fatalf("a hand-in failure was recorded as a provider execution failure: %+v", f)
		}
	}
	if !found {
		t.Fatalf("no HAND_IN failure recorded: %+v", completed.Failures)
	}
}
