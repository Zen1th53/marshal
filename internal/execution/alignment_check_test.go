package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/alignment"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestAlignmentScopeViolationRefused(t *testing.T) {
	repo := testgit.New(t)
	ctx := context.Background()

	cfg := EngineConfig{
		ProjectRoot: repo.Path(),
		DefaultTTL:  1 * time.Minute,
	}
	engine, err := NewEngine(cfg, nil, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	mockHarness := NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, worktree string) (TaskResult, error) {
		if task.TaskID == "task-2" {
			// Worker writes a file outside the task scope
			_ = os.WriteFile(filepath.Join(worktree, "outside.txt"), []byte("unauthorized write"), 0644)
		} else if len(task.TargetFiles) > 0 {
			_ = os.WriteFile(filepath.Join(worktree, task.TargetFiles[0]), []byte(`{"ok": true}`), 0644)
		}
		return TaskResult{
			TaskID:  task.TaskID,
			Success: true,
		}, nil
	})
	engine.RegisterHarness(mockHarness)

	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)
	// Restrict goal scope strictly to declared files
	goal.Scope = []string{"config.json", "policy.go", "policy_test.go"}

	handoff := createTestHandoff(t, repo.Path(), goal, p)
	run, err := engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		t.Fatalf("InitializeRun failed: %v", err)
	}

	executedRun, err := engine.ExecuteRun(ctx, run.RunID)
	if err == nil {
		t.Fatal("expected ExecuteRun to fail due to scope violation, got nil error")
	}

	task2 := executedRun.Tasks["task-2"]
	if task2.State != TaskFailed {
		t.Fatalf("expected task-2 state FAILED, got: %s", task2.State)
	}
	if !strings.Contains(task2.LastFailureReason, "unpermitted delivery file outside.txt") {
		t.Fatalf("expected failure reason to cite unpermitted delivery file, got: %s", task2.LastFailureReason)
	}
	if task2.Alignment == nil {
		t.Fatal("expected task-2 to have alignment record, got nil")
	}

	hasScopeLock := false
	for _, v := range task2.Alignment.Result.Violations {
		if v.Type == alignment.CheckScopeLock {
			hasScopeLock = true
			break
		}
	}
	if !hasScopeLock {
		t.Fatalf("expected CheckScopeLock violation in alignment result, got: %+v", task2.Alignment.Result.Violations)
	}
}

func TestAlignmentSemanticDriftAllowedAsWarning(t *testing.T) {
	repo := testgit.New(t)
	ctx := context.Background()

	cfg := EngineConfig{
		ProjectRoot: repo.Path(),
		DefaultTTL:  1 * time.Minute,
	}
	engine, err := NewEngine(cfg, nil, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	mockHarness := NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, worktree string) (TaskResult, error) {
		// Clean execution writing within scope
		if len(task.TargetFiles) > 0 {
			_ = os.WriteFile(filepath.Join(worktree, task.TargetFiles[0]), []byte(`{"ok": true}`), 0644)
		}
		return TaskResult{
			TaskID:  task.TaskID,
			Success: true,
		}, nil
	})
	engine.RegisterHarness(mockHarness)

	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)
	goal.Scope = []string{"config.json", "policy.go", "policy_test.go"}

	handoff := createTestHandoff(t, repo.Path(), goal, p)
	run, err := engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		t.Fatalf("InitializeRun failed: %v", err)
	}

	// Active goal revision is now 2, while task executed against revision 1 -> causes CheckGoalDrift
	goal.Revision = 2
	engine.mu.Lock()
	engine.cachedGoal[run.RunID] = goal
	engine.mu.Unlock()

	executedRun, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("ExecuteRun should not fail on semantic drift warning: %v, failures: %+v", err, executedRun.Failures)
	}

	task1 := executedRun.Tasks["task-1"]
	if task1.State == TaskFailed {
		t.Fatalf("task-1 should not fail on semantic drift warning, got FAILED: %s", task1.LastFailureReason)
	}
	if task1.Alignment == nil {
		t.Fatal("expected alignment record on task-1")
	}

	hasGoalDrift := false
	for _, v := range task1.Alignment.Result.Violations {
		if v.Type == alignment.CheckGoalDrift {
			hasGoalDrift = true
			break
		}
	}
	if !hasGoalDrift {
		t.Fatalf("expected CheckGoalDrift violation in alignment, got: %+v", task1.Alignment.Result.Violations)
	}
}

func TestReconcileFilesRefusesEmptyScope(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("initial"), 0644)

	wm, err := NewWorktreeManager(tmpDir)
	if err != nil {
		t.Fatalf("failed to create WorktreeManager: %v", err)
	}

	wtPath, err := wm.PrepareWorktree(ctx, "task-empty-scope", "run-1")
	if err != nil {
		t.Fatalf("PrepareWorktree failed: %v", err)
	}
	defer wm.CleanWorktree(ctx, wtPath)

	_ = os.WriteFile(filepath.Join(wtPath, "file.txt"), []byte("modified by worker"), 0644)

	// Reconcile with nil scope must fail closed
	if _, err := wm.ReconcileChanges(wtPath, nil); err == nil || !errors.Is(err, ErrIsolationCompromised) {
		t.Fatalf("expected ErrIsolationCompromised for nil scope, got: %v", err)
	}

	// Reconcile with empty slice scope must also fail closed
	if _, err := wm.ReconcileChanges(wtPath, []string{}); err == nil || !errors.Is(err, ErrIsolationCompromised) {
		t.Fatalf("expected ErrIsolationCompromised for empty slice scope, got: %v", err)
	}
}
