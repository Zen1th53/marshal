//go:build !governed

package execution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtremeFailedDiagnosticCannotPromoteAfterReopenAndFailover(t *testing.T) {
	e, parent, base := laneFixture(t)
	lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	e.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, wt string) (TaskResult, error) {
		return TaskResult{TaskID: task.TaskID, ErrorMessage: "original failure", EvidenceList: []ExecutionEvidence{{EvidenceID: "EVID-failed", TaskID: task.TaskID, OutputDigest: "sha256:" + strings.Repeat("a", 64)}}}, nil
	}))
	failed, _ := e.ExecuteTaskBound(t.Context(), lane.RunID, "task-1", base)
	if failed == nil || failed.Tasks["task-1"].State != TaskFailed || len(failed.Evidence) != 1 {
		t.Fatal("original failed diagnostic not captured")
	}
	store, err := NewFileRunStore(e.store.(*FileRunStore).baseDir)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := NewEngineWithReaders(e.cfg, store, nil, e.planReader, e.goalReader)
	if err != nil {
		t.Fatal(err)
	}
	fresh.RegisterHarness(NewMockHarness("retry", func(_ context.Context, task TaskExecution, _ ConstraintPackage, wt string) (TaskResult, error) {
		if err := os.WriteFile(filepath.Join(wt, "retry.txt"), []byte("later work\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return TaskResult{TaskID: task.TaskID, Success: true, EvidenceList: []ExecutionEvidence{{EvidenceID: "EVID-failed", TaskID: task.TaskID, OutputDigest: "sha256:" + strings.Repeat("b", 64)}}}, nil
	}))
	rebound, err := fresh.RebindProviderFailover(WithVerifiedFallbackModel(t.Context(), "retry", "verified-retry"), failed.RunID, failed.Version, "task-1", "mock", "retry", base, "fixture-reviewed-failover", func(ExecutionRun, TaskExecution) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	result, _ := fresh.ExecuteTaskBound(t.Context(), rebound.RunID, "task-1", base)
	if result == nil || result.Tasks["task-1"].State == TaskCompletedPendingVerify {
		t.Fatal("reopened failed diagnostic became later successful evidence")
	}
	if len(result.Evidence) != 1 || result.Evidence[0].Status != EvidenceInvalidated || result.Evidence[0].Type != "failed_tool_output" {
		t.Fatal("historical failed diagnostic was rewritten")
	}
	if _, err := fresh.EvidenceOracle().GetEvidence("EVID-failed"); err == nil {
		t.Fatal("promoted diagnostic entered fresh oracle as successful output")
	}
}

func TestFailedDiagnosticProviderAliasesRetainAdmittedIdentity(t *testing.T) {
	for _, pair := range [][2]string{{"claude-code", "claude"}, {"antigravity", "agy"}, {"codex", "codex"}} {
		t.Run(pair[0], func(t *testing.T) {
			engine, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			run := ExecutionRun{RunID: "run-alias"}
			task := TaskExecution{TaskID: "task-alias", CanonicalTaskID: "canonical", AssignedHarness: pair[0], Attempts: 2}
			ev := ExecutionEvidence{EvidenceID: "EVID-alias", TaskID: task.TaskID, OutputDigest: "sha256:" + strings.Repeat("a", 64), ProviderObservation: &ProviderObservation{Provider: pair[1], RunID: run.RunID, TaskID: task.TaskID, CanonicalTaskID: task.CanonicalTaskID, Attempt: task.Attempts, TimingBasis: "unknown"}}
			if err := engine.captureFailedDiagnostics(&run, &task, []ExecutionEvidence{ev}); err != nil {
				t.Fatal(err)
			}
			if run.Evidence[0].ProviderObservation.Provider != pair[1] || run.Evidence[0].ProviderObservation.Model != "" {
				t.Fatal("admitted alias or unknown emitted model changed")
			}
		})
	}
}
