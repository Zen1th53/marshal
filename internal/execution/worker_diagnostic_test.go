package execution

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunFailureKeepsProviderDiagnosticAfterReload(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileRunStore(filepath.Join(root, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(EngineConfig{ProjectRoot: root}, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	message := "Codex execution did not complete successfully; stderr: first: connecting; last: TLS certificate invalid"
	engine.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		return TaskResult{TaskID: task.TaskID, ErrorMessage: message}, nil
	}))
	goal, plan := createTestGoalAndPlan(time.Now().UTC())
	run, err := engine.InitializeRun(t.Context(), createTestHandoff(t, root, goal, plan), goal, plan)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = engine.ExecuteRun(t.Context(), run.RunID)
	reloaded, err := engine.GetRun(t.Context(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Tasks["task-1"].LastFailureReason; got != message {
		t.Fatalf("task failure lost stderr: %q", got)
	}
	for _, failure := range reloaded.Failures {
		if failure.Stage == "EXECUTE" && strings.Contains(failure.Reason, "first: connecting") && strings.Contains(failure.Reason, "last: TLS certificate invalid") {
			return
		}
	}
	t.Fatal("run failure record lost provider diagnostics")
}
