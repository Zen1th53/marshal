//go:build !governed

package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestExtremeProviderFailoverRebindsCanonicalFailedTask(t *testing.T) {
	ctx := WithVerifiedFallbackModel(context.Background(), "codex", "verified-codex")
	repo := testgit.New(t)
	engine, err := NewEngine(EngineConfig{ProjectRoot: repo.Path()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	goal, p := createTestGoalAndPlan(time.Now().UTC())
	engine.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, path string) (TaskResult, error) {
		if err := os.WriteFile(filepath.Join(path, "partial.txt"), []byte("limited attempt"), 0600); err != nil {
			return TaskResult{}, err
		}
		return TaskResult{}, errors.New("provider rate limit exceeded")
	}))
	run, err := engine.InitializeRun(ctx, createTestHandoff(t, repo.Path(), goal, p), goal, p)
	if err != nil {
		t.Fatal(err)
	}
	base := repo.HEAD(t)
	if err = engine.SetPreserveBranch(ctx, run.RunID, base); err != nil {
		t.Fatal(err)
	}
	failed, err := engine.ExecuteTaskBound(ctx, run.RunID, "task-1", base)
	if err == nil || failed.Tasks["task-1"].State != TaskFailed {
		t.Fatalf("provider did not fail: %v %+v", err, failed)
	}
	old := failed.Tasks["task-1"]
	engine.RegisterHarness(NewMockHarness("codex", func(_ context.Context, task TaskExecution, _ ConstraintPackage, path string) (TaskResult, error) {
		if _, err := os.Stat(filepath.Join(path, "partial.txt")); !os.IsNotExist(err) {
			return TaskResult{}, errors.New("failed provider files carried into retry")
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	if _, err = engine.RebindProviderFailover(ctx, run.RunID, failed.Version, "task-1", "mock", "codex", base, "receipt", nil); err == nil {
		t.Fatal("missing authority accepted")
	}
	if _, err = engine.RebindProviderFailover(ctx, run.RunID, failed.Version-1, "task-1", "mock", "codex", base, "receipt", func(ExecutionRun, TaskExecution) error { return nil }); err == nil {
		t.Fatal("stale run revision accepted")
	}
	deny := errors.New("no recorded failover")
	if _, err = engine.RebindProviderFailover(ctx, run.RunID, failed.Version, "task-1", "mock", "codex", base, "receipt", func(ExecutionRun, TaskExecution) error { return deny }); !errors.Is(err, deny) {
		t.Fatalf("authority refusal lost: %v", err)
	}
	authorize := func(current ExecutionRun, task TaskExecution) error {
		if current.PlanID != run.PlanID || current.PlanVersion != run.PlanVersion || task.TaskID != old.TaskID || task.CanonicalTaskID != old.CanonicalTaskID || task.BaseCommit != base {
			return errors.New("canonical identity changed")
		}
		return nil
	}
	rebound, err := engine.RebindProviderFailover(ctx, run.RunID, failed.Version, "task-1", "mock", "codex", base, "receipt", authorize)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.Tasks["task-1"].State != TaskReady || rebound.Tasks["task-1"].AssignedHarness != "codex" || rebound.Tasks["task-1"].AssignedModel != "verified-codex" || rebound.BudgetConsumed != failed.BudgetConsumed || len(rebound.Failures) != len(failed.Failures) {
		t.Fatal("retry changed budget history or canonical state")
	}
	if _, err = os.Stat(old.WorktreePath + "-limited-1/partial.txt"); err != nil {
		t.Fatal("partial evidence lost", err)
	}
	replay, err := engine.RebindProviderFailover(ctx, run.RunID, rebound.Version, "task-1", "mock", "codex", base, "receipt", authorize)
	if err != nil || replay.Version != rebound.Version {
		t.Fatalf("receipt replay did not converge: %v", err)
	}
	completed, err := engine.ExecuteTaskBound(ctx, run.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	task := completed.Tasks["task-1"]
	if task.State != TaskCompletedPendingVerify || task.Attempts != 2 || task.TaskID != old.TaskID || task.CanonicalTaskID != old.CanonicalTaskID || task.WorktreePath != old.WorktreePath {
		t.Fatalf("retry lost canonical identity: %+v", task)
	}
	if completed.Tasks["task-2"].State != failed.Tasks["task-2"].State {
		t.Fatal("retry ran an unselected sibling")
	}
	if _, err = engine.RebindProviderFailover(ctx, run.RunID, completed.Version, "task-1", "codex", "mock", base, "forged", authorize); err == nil {
		t.Fatal("completed task accepted receipt replay")
	}
}

func TestExtremeProviderFailoverBindsVerifiedAGYModel(t *testing.T) {
	ctx := WithVerifiedFallbackModel(t.Context(), "agy", "verified-gemini-selection")
	repo := testgit.New(t)
	engine, err := NewEngine(EngineConfig{ProjectRoot: repo.Path()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	goal, p := createTestGoalAndPlan(time.Now().UTC())
	run, err := engine.InitializeRun(ctx, createTestHandoff(t, repo.Path(), goal, p), goal, p)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	engine.RegisterHarness(NewMockHarness("agy", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		called = true
		if task.AssignedModel != "verified-gemini-selection" {
			return TaskResult{}, errors.New("selected AGY model lost")
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	authorize := func(ExecutionRun, TaskExecution) error { return nil }
	if _, err := engine.RebindProviderFailover(t.Context(), run.RunID, run.Version, "task-1", "mock", "agy", repo.HEAD(t), "selection", authorize); err == nil {
		t.Fatal("model-free failover accepted")
	}
	rebound, err := engine.RebindProviderFailover(ctx, run.RunID, run.Version, "task-1", "mock", "agy", repo.HEAD(t), "selection", authorize)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.Tasks["task-1"].AssignedModel != "verified-gemini-selection" {
		t.Fatal("durable model lost")
	}
	if err := engine.SetPreserveBranch(ctx, run.RunID, repo.HEAD(t)); err != nil {
		t.Fatal(err)
	}
	completed, err := engine.ExecuteTaskBound(ctx, run.RunID, "task-1", repo.HEAD(t))
	if err != nil || !called || completed.Tasks["task-1"].State != TaskCompletedPendingVerify {
		t.Fatalf("rebound AGY did not execute: %v called=%v", err, called)
	}
	wrong := WithVerifiedFallbackModel(t.Context(), "agy", "another-model")
	if _, err := engine.RebindProviderFailover(wrong, run.RunID, rebound.Version, "task-1", "mock", "agy", repo.HEAD(t), "selection", authorize); err == nil {
		t.Fatal("changed replay model accepted")
	}
}
