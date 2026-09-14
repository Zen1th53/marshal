package execution

import (
	"context"
	"errors"
	"testing"
)

func TestCodexNativeHarnessNeverSynthesizesSuccessWithoutExecutor(t *testing.T) {
	harness := NewCodexNativeHarness("gpt-5.6-terra")
	if got := harness.Name(); got != "codex" {
		t.Fatalf("Codex harness route name = %q, want public plan route %q", got, "codex")
	}
	result, err := harness.Execute(context.Background(), TaskExecution{TaskID: "TASK-1"}, ConstraintPackage{}, t.TempDir())
	if err == nil {
		t.Fatalf("unbound harness returned synthetic success: %#v", result)
	}
	if result.Success || !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("unbound harness must fail closed, result=%#v err=%v", result, err)
	}
}

func TestCodexNativeHarnessDelegatesToBoundExecutor(t *testing.T) {
	called := false
	harness := NewCodexNativeHarnessWithExecutor("gpt-5.6-terra", func(_ context.Context, task TaskExecution, _ ConstraintPackage, worktree string) (TaskResult, error) {
		called = true
		if task.TaskID != "TASK-2" || worktree == "" {
			t.Fatalf("executor lost task/worktree binding: %#v %q", task, worktree)
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	})
	result, err := harness.Execute(context.Background(), TaskExecution{TaskID: "TASK-2"}, ConstraintPackage{}, t.TempDir())
	if err != nil || !called || !result.Success {
		t.Fatalf("bound executor did not run: result=%#v called=%v err=%v", result, called, err)
	}
}

func TestUnknownHarnessCannotFallBackToMock(t *testing.T) {
	engine, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir()}, NewMemoryRunStore(), NewMemoryJournalStore())
	if err != nil {
		t.Fatal(err)
	}
	if harness, err := engine.GetHarness("unknown-provider"); err == nil || harness != nil || !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("unknown provider must fail closed rather than use mock: harness=%T err=%v", harness, err)
	}
}
