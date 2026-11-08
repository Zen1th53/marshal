package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailedExecutionRetainsDiagnosticEvidenceWithoutCompletion(t *testing.T) {
	for _, runnerError := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsuccessful_exit_zero", true: "runner_error"}[runnerError], func(t *testing.T) {
			root := t.TempDir()
			store, err := NewFileRunStore(filepath.Join(root, "runs"))
			if err != nil {
				t.Fatal(err)
			}
			engine, err := NewEngine(EngineConfig{ProjectRoot: root}, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			var original *ProviderObservation
			engine.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, wt string) (TaskResult, error) {
				if err := os.WriteFile(filepath.Join(wt, "failed-marker.txt"), []byte("partial work\n"), 0600); err != nil {
					t.Fatal(err)
				}
				start, end := time.Now().Add(-time.Second).UTC(), time.Now().UTC()
				original = &ProviderObservation{Provider: "mock", RunID: task.RunID, TaskID: task.TaskID, CanonicalTaskID: task.CanonicalTaskID, Attempt: task.Attempts, AttemptBasis: "execution_engine_admission", TimingBasis: "adapter_process", StartedAt: &start, EndedAt: &end}
				result := TaskResult{TaskID: task.TaskID, Success: false, ErrorMessage: "native final failure", EvidenceList: []ExecutionEvidence{{EvidenceID: "EVID-failed", RunID: task.RunID, TaskID: task.TaskID, ProviderObservation: original, ExitCode: 0, StdoutSummary: "raw provider text must not persist", StderrSummary: "raw error must not persist", OutputDigest: "sha256:" + strings.Repeat("a", 64), CreatedAt: end}}, Claims: []ExecutionClaim{{ClaimID: "worker-claim"}}}
				if runnerError {
					return result, errors.New("native runner failure")
				}
				return result, nil
			}))
			goal, plan := createTestGoalAndPlan(time.Now().UTC())
			run, err := engine.InitializeRun(t.Context(), createTestHandoff(t, root, goal, plan), goal, plan)
			if err != nil {
				t.Fatal(err)
			}
			completed, _ := engine.ExecuteRun(t.Context(), run.RunID)
			if completed == nil {
				t.Fatal("missing failed run")
			}
			task := completed.Tasks["task-1"]
			if task.State != TaskFailed || task.ResultCommit != "" || !strings.Contains(task.LastFailureReason, "failure") {
				t.Fatalf("failure became completion: %+v", task)
			}
			if len(completed.Evidence) != 1 || len(task.CollectedEvidence) != 1 {
				t.Fatalf("failed runtime evidence was dropped: refs=%d ids=%d", len(completed.Evidence), len(task.CollectedEvidence))
			}
			ref := completed.Evidence[0]
			if ref.Type != "failed_tool_output" || ref.Status != EvidenceInvalidated {
				t.Fatalf("diagnostic promoted to completion evidence: %+v", ref)
			}
			captured, err := engine.EvidenceOracle().GetEvidence("EVID-failed")
			if err != nil || captured.Status != EvidenceInvalidated || captured.StdoutSummary != "" || captured.StderrSummary != "" {
				t.Fatalf("unsafe diagnostic capture: %+v err=%v", captured, err)
			}
			*original.StartedAt = time.Time{}
			if ref.ProviderObservation.StartedAt.IsZero() {
				t.Fatal("caller mutated retained diagnostic")
			}
			if _, err := engine.EvidenceOracle().RecordEvidence(ExecutionEvidence{EvidenceID: "EVID-failed", Status: EvidenceValid}); err == nil {
				t.Fatal("later success overwrote failed diagnostic identity")
			}
			claim, err := engine.EvidenceOracle().RecordClaim(ExecutionClaim{ClaimID: "later-success-assertion", EvidenceRefs: []string{"EVID-failed"}})
			if err != nil || claim.Status == ClaimVerified || claim.Status == ClaimSupported {
				t.Fatal("failed diagnostic substantiated a later success claim")
			}
			reopened, err := NewFileRunStore(filepath.Join(root, "runs"))
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := reopened.GetRun(t.Context(), run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if len(persisted.Evidence) != 1 || persisted.Tasks["task-1"].State != TaskFailed {
				t.Fatal("failed diagnostic was not durable")
			}
			raw, _ := json.Marshal(persisted)
			if strings.Contains(string(raw), "raw provider text") || strings.Contains(string(raw), "raw error") || strings.Contains(string(raw), "worker-claim") {
				t.Fatal("raw output or unsupported claim entered durable failed record")
			}
		})
	}
}

func TestFailedExecutionDiagnosticBatchRefusesMalformedOrForeignRecords(t *testing.T) {
	cases := []struct {
		name   string
		change func([]ExecutionEvidence, TaskExecution) []ExecutionEvidence
	}{
		{"duplicate", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].EvidenceID = v[0].EvidenceID
			return v
		}},
		{"oversize", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			for len(v) < 17 {
				ev := v[0]
				ev.EvidenceID += strings.Repeat("x", len(v))
				v = append(v, ev)
			}
			return v
		}},
		{"empty_id", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence { v[1].EvidenceID = ""; return v }},
		{"long_id", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].EvidenceID = strings.Repeat("x", 257)
			return v
		}},
		{"control_id", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].EvidenceID = "EVID-\n"
			return v
		}},
		{"foreign_run", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence { v[1].RunID = "other"; return v }},
		{"foreign_task", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence { v[1].TaskID = "other"; return v }},
		{"foreign_observation_run", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].ProviderObservation.RunID = "other"
			return v
		}},
		{"foreign_observation_task", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].ProviderObservation.TaskID = "other"
			return v
		}},
		{"foreign_canonical_task", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].ProviderObservation.CanonicalTaskID = "other"
			return v
		}},
		{"foreign_provider", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].ProviderObservation.Provider = "claude"
			return v
		}},
		{"future_capture", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].CreatedAt = time.Now().Add(time.Hour)
			return v
		}},
		{"capture_before_interval_end", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			start, end := time.Now().Add(-time.Second), time.Now()
			v[1].ProviderObservation.TimingBasis = "adapter_process"
			v[1].ProviderObservation.StartedAt, v[1].ProviderObservation.EndedAt = &start, &end
			v[1].CreatedAt = start
			return v
		}},
		{"future_interval", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			start, end := time.Now(), time.Now().Add(time.Hour)
			v[1].ProviderObservation.TimingBasis = "adapter_process"
			v[1].ProviderObservation.StartedAt, v[1].ProviderObservation.EndedAt = &start, &end
			return v
		}},
		{"foreign_admission", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].ProviderObservation.Attempt++
			return v
		}},
		{"malformed_observation", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].ProviderObservation.TimingBasis = "invented"
			return v
		}},
		{"malformed_digest", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].OutputDigest = "sha256:" + strings.Repeat("g", 64)
			return v
		}},
		{"malformed_exit", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence { v[1].ExitCode = 256; return v }},
		{"negative_duration", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence { v[1].DurationMs = -1; return v }},
		{"unserializable_time", func(v []ExecutionEvidence, _ TaskExecution) []ExecutionEvidence {
			v[1].CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
			return v
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			engine.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
				batch := []ExecutionEvidence{}
				for _, id := range []string{"EVID-first", "EVID-second"} {
					batch = append(batch, ExecutionEvidence{EvidenceID: id, RunID: task.RunID, TaskID: task.TaskID, OutputDigest: "sha256:" + strings.Repeat("a", 64), ProviderObservation: &ProviderObservation{Provider: "mock", RunID: task.RunID, TaskID: task.TaskID, CanonicalTaskID: task.CanonicalTaskID, Attempt: task.Attempts, TimingBasis: "unknown"}})
				}
				return TaskResult{TaskID: task.TaskID, Success: false, ErrorMessage: "original failure", EvidenceList: tc.change(batch, task)}, nil
			}))
			goal, plan := createTestGoalAndPlan(time.Now().UTC())
			run, err := engine.InitializeRun(t.Context(), createTestHandoff(t, engine.cfg.ProjectRoot, goal, plan), goal, plan)
			if err != nil {
				t.Fatal(err)
			}
			failed, _ := engine.ExecuteRun(t.Context(), run.RunID)
			if failed == nil {
				t.Fatal("no failed run")
			}
			task := failed.Tasks["task-1"]
			if task.State != TaskFailed || task.ResultCommit != "" || !strings.HasPrefix(task.LastFailureReason, "original failure") || !strings.Contains(task.LastFailureReason, "diagnostic capture refused") {
				t.Fatal("failure/diagnostic refusal lost")
			}
			if len(failed.Evidence) != 0 || len(task.CollectedEvidence) != 0 || len(engine.EvidenceOracle().ListEvidenceForRun(run.RunID)) != 0 {
				t.Fatal("malformed batch was partially captured")
			}
		})
	}
}

func TestFailedDiagnosticIdentityCollisionIsAtomic(t *testing.T) {
	engine, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := engine.EvidenceOracle().RecordEvidence(ExecutionEvidence{EvidenceID: "EVID-second", OutputDigest: "original", Status: EvidenceValid})
	if err != nil {
		t.Fatal(err)
	}
	engine.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		return TaskResult{TaskID: task.TaskID, Success: false, ErrorMessage: "original failure", EvidenceList: []ExecutionEvidence{{EvidenceID: "EVID-first", TaskID: task.TaskID, OutputDigest: "sha256:" + strings.Repeat("a", 64)}, {EvidenceID: "EVID-second", TaskID: task.TaskID, OutputDigest: "sha256:" + strings.Repeat("b", 64)}}}, nil
	}))
	goal, plan := createTestGoalAndPlan(time.Now().UTC())
	run, err := engine.InitializeRun(t.Context(), createTestHandoff(t, engine.cfg.ProjectRoot, goal, plan), goal, plan)
	if err != nil {
		t.Fatal(err)
	}
	failed, _ := engine.ExecuteRun(t.Context(), run.RunID)
	if failed == nil || len(failed.Evidence) != 0 {
		t.Fatal("colliding batch partially persisted")
	}
	if _, err := engine.EvidenceOracle().GetEvidence("EVID-first"); err == nil {
		t.Fatal("first batch member leaked before collision refusal")
	}
	got, err := engine.EvidenceOracle().GetEvidence("EVID-second")
	if err != nil || got.OutputDigest != prior.OutputDigest || got.Status != EvidenceValid {
		t.Fatal("collision replaced original evidence")
	}
}

func TestFailedDiagnosticSixteenRecordBoundary(t *testing.T) {
	engine, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	engine.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		result := TaskResult{TaskID: task.TaskID, ErrorMessage: "original failure"}
		for i := 0; i < 16; i++ {
			result.EvidenceList = append(result.EvidenceList, ExecutionEvidence{EvidenceID: fmt.Sprintf("EVID-%02d", i), TaskID: task.TaskID, OutputDigest: "sha256:" + strings.Repeat("a", 64)})
		}
		return result, nil
	}))
	goal, plan := createTestGoalAndPlan(time.Now().UTC())
	run, err := engine.InitializeRun(t.Context(), createTestHandoff(t, engine.cfg.ProjectRoot, goal, plan), goal, plan)
	if err != nil {
		t.Fatal(err)
	}
	failed, _ := engine.ExecuteRun(t.Context(), run.RunID)
	if failed == nil || failed.Tasks["task-1"].State != TaskFailed || failed.Tasks["task-1"].ResultCommit != "" || len(failed.Evidence) != 16 || len(engine.EvidenceOracle().ListEvidenceForRun(run.RunID)) != 16 {
		t.Fatal("bounded diagnostic capture lost failure or records")
	}
}

func TestFailedNativeProcessOutcomeSurvivesDurableReopenWithoutSuccess(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileRunStore(filepath.Join(root, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(EngineConfig{ProjectRoot: root}, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	outcome := &ProviderProcessOutcome{Status: "failure", ExitCode: 7, NativeEnvelope: "success"}
	e.RegisterHarness(NewMockHarness("agy", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		return TaskResult{TaskID: task.TaskID, ErrorMessage: "original failed native process", EvidenceList: []ExecutionEvidence{{EvidenceID: "EVID-process-failed", TaskID: task.TaskID, ExitCode: 7, OutputDigest: "sha256:" + strings.Repeat("a", 64), ProviderObservation: &ProviderObservation{Provider: "agy", RunID: task.RunID, TaskID: task.TaskID, CanonicalTaskID: task.CanonicalTaskID, Attempt: task.Attempts, TimingBasis: "unknown", ProcessOutcome: outcome}}}}, nil
	}))
	goal, p := createTestGoalAndPlan(time.Now().UTC())
	for i := range p.Assignments.Assignments {
		p.Assignments.Assignments[i].Harness = "agy"
	}
	run, err := e.InitializeRun(t.Context(), createTestHandoff(t, root, goal, p), goal, p)
	if err != nil {
		t.Fatal(err)
	}
	failed, _ := e.ExecuteRun(t.Context(), run.RunID)
	if failed == nil || failed.Tasks["task-1"].State != TaskFailed || failed.Tasks["task-1"].ResultCommit != "" || len(failed.Evidence) != 1 {
		t.Fatal("failed native outcome became completion or disappeared")
	}
	outcome.ExitCode = 0
	if failed.Evidence[0].ProviderObservation.ProcessOutcome.ExitCode != 7 {
		t.Fatal("caller mutated retained outcome")
	}
	reopened, err := NewFileRunStore(filepath.Join(root, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := reopened.GetRun(t.Context(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	ref := saved.Evidence[0]
	if ref.Status != EvidenceInvalidated || ref.Type != "failed_tool_output" || ref.ProviderObservation.ProcessOutcome.ExitCode != 7 || ref.ProviderObservation.Model != "" {
		t.Fatal("durable finite outcome promoted or fabricated model")
	}
}

func TestFailedDiagnosticRefusesExitOutcomeMismatchBeforeAnyWrite(t *testing.T) {
	e, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := ExecutionRun{RunID: "run"}
	task := TaskExecution{TaskID: "task", AssignedHarness: "agy", Attempts: 2}
	observation := &ProviderObservation{Provider: "agy", RunID: "run", TaskID: "task", Attempt: 2, TimingBasis: "unknown", ProcessOutcome: &ProviderProcessOutcome{Status: "failure", ExitCode: 7, NativeEnvelope: "success"}}
	batch := []ExecutionEvidence{{EvidenceID: "EVID-first", TaskID: "task", OutputDigest: "sha256:" + strings.Repeat("a", 64)}, {EvidenceID: "EVID-second", TaskID: "task", ExitCode: 0, OutputDigest: "sha256:" + strings.Repeat("a", 64), ProviderObservation: observation}}
	if e.captureFailedDiagnostics(&run, &task, batch) == nil || len(run.Evidence) != 0 || len(e.oracle.ListEvidenceForRun("run")) != 0 {
		t.Fatal("contradictory diagnostic batch partially persisted")
	}
}
