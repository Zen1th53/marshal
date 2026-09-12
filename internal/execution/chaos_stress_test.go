package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/plan"
)

// =========================================================================
// File 44: Mutation & Fault Injection Suite
// =========================================================================

func TestMutation_CASCheckEnforcement(t *testing.T) {
	store := NewMemoryRunStore()
	ctx := context.Background()

	run := ExecutionRun{
		RunID:     "run-cas-mut",
		PlanID:    "plan-cas-mut",
		Version:   1,
		State:     RunReady,
		ProjectID: testProjectID,
		Tasks:     make(map[string]TaskExecution),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	// Try updating with stale version 0
	staleRun := run
	staleRun.Version = 0
	staleRun.State = RunRunning
	err := store.UpdateRun(ctx, staleRun)
	if err == nil {
		t.Fatalf("expected CAS error when saving stale version, got nil")
	}
	if !errors.Is(err, ErrRunConflict) {
		t.Fatalf("expected ErrRunConflict, got: %v", err)
	}
}

func TestMutation_LeaseOwnershipCheckEnforcement(t *testing.T) {
	lm := NewLeaseManager()
	now := time.Now().UTC()

	lease1, err := lm.AcquireLease(AcquireLeaseRequest{
		RunID:           "run-1",
		TaskID:          "task-1",
		AgentID:         "worker-A",
		Role:            "developer",
		ScopedResources: []string{"fileA.go"},
		TTL:             5 * time.Second,
		Now:             now,
	})
	if err != nil {
		t.Fatalf("AcquireLease failed: %v", err)
	}

	// Another worker attempts to acquire overlapping resource
	_, err = lm.AcquireLease(AcquireLeaseRequest{
		RunID:           "run-1",
		TaskID:          "task-2",
		AgentID:         "worker-B",
		Role:            "developer",
		ScopedResources: []string{"fileA.go"},
		TTL:             5 * time.Second,
		Now:             now,
	})
	if err == nil {
		t.Fatalf("expected lease collision error, got nil")
	}
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("expected ErrLeaseConflict, got: %v", err)
	}

	// Worker B attempts to release Worker A's lease
	err = lm.ReleaseLease(lease1.LeaseID, "worker-B", now)
	if err == nil {
		t.Fatalf("expected error when releasing unowned lease, got nil")
	}
	if !errors.Is(err, ErrUnauthorizedWorker) {
		t.Fatalf("expected ErrUnauthorizedWorker, got: %v", err)
	}
}

func TestMutation_ApprovalGateEnforcement(t *testing.T) {
	am := NewApprovalManager()
	now := time.Now().UTC()

	actionDigest := ComputeActionDigest("database_migration", "schema.sql", "DROP TABLE users;", "")
	stateDigest := ComputeStateDigest("clean")

	// Attempting to consume approval before it exists must fail
	err := am.ValidateAndConsume("appr-nonexistent", actionDigest, stateDigest, now)
	if err == nil {
		t.Fatalf("expected error consuming nonexistent approval, got nil")
	}

	// Request approval and verify it cannot be consumed before being decided
	appReq, err := am.RequestApproval(ApprovalRequest{
		RunID:          "run-appr-mut",
		TaskID:         "task-appr-mut",
		PlanID:         "plan-1",
		PlanVersion:    1,
		OperationType:  "database_migration",
		TargetResource: "schema.sql",
		DiffPreview:    "DROP TABLE users;",
		CurrentState:   "clean",
		TTL:            5 * time.Minute,
		Now:            now,
	})
	if err != nil {
		t.Fatalf("RequestApproval failed: %v", err)
	}

	err = am.ValidateAndConsume(appReq.ApprovalID, actionDigest, stateDigest, now)
	if err == nil {
		t.Fatalf("expected error consuming undecided approval, got nil")
	}
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got: %v", err)
	}
}

func TestMutation_ConstraintReinjectionIntegrity(t *testing.T) {
	pkg := ConstraintPackage{
		GoalOutcome:             "Outcome",
		TaskID:                  "task-1",
		TaskDescription:         "Description",
		HardConstraints:         []string{"No external calls", "Deterministic outputs"},
		DoNotDo:                 []string{"touch prod"},
		ApprovalRequired:        false,
		ExpectedOutputs:         []string{"out.json"},
		VerificationObligations: []string{"tests pass"},
	}

	digest := ComputeConstraintDigest(pkg)
	pkg.Digest = digest

	if err := VerifyConstraintPackage(pkg); err != nil {
		t.Fatalf("valid package failed: %v", err)
	}

	// Mutate constraints without updating digest
	pkg.HardConstraints = append(pkg.HardConstraints, "Tampered constraint")
	err := VerifyConstraintPackage(pkg)
	if err == nil {
		t.Fatalf("expected error for mutated constraint package, got nil")
	}
	if !errors.Is(err, ErrConstraintViolation) {
		t.Fatalf("expected ErrConstraintViolation, got: %v", err)
	}
}

func TestMutation_Process04EntryGateEnforcement(t *testing.T) {
	pReader := NewMemoryPlanReader()
	gReader := NewMemoryGoalReader()
	gate := NewEntryGate(pReader, gReader)

	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)
	handoff := createTestHandoff(t, t.TempDir(), goal, p)

	// Mutate plan to Draft state (unapproved)
	unapprovedPlan := p
	unapprovedPlan.ID = "PLAN-UNAPPROVED"
	unapprovedPlan.State = plan.StateDraft
	pReader.AddPlan(unapprovedPlan)
	gReader.AddGoal(goal)
	handoff.PlanID = unapprovedPlan.ID

	err := gate.ValidateHandoff(context.Background(), handoff, now)
	if err == nil {
		t.Fatalf("expected error for unapproved plan handoff, got nil")
	}
	var refusal GateRefusal
	if !errors.As(err, &refusal) || refusal.Code != ReasonPlanNotApproved {
		t.Fatalf("expected GateRefusal with ReasonPlanNotApproved, got: %v", err)
	}
}

func TestMutation_Process06HandoffGateEnforcement(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := EngineConfig{
		ProjectRoot: tmpDir,
		MaxWorkers:  2,
		DefaultTTL:  1 * time.Minute,
	}
	engine, err := NewEngine(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	ctx := context.Background()

	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)
	handoff := createTestHandoff(t, tmpDir, goal, p)

	run, err := engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		t.Fatalf("InitializeRun failed: %v", err)
	}

	// Attempt to assemble Process 06 bundle while tasks are still in Pending state
	_, err = engine.AssembleProcess06Bundle(ctx, run.RunID)
	if err == nil {
		t.Fatalf("expected error assembling Process 06 bundle with unfinished tasks, got nil")
	}
	if !errors.Is(err, ErrRunInvalid) {
		t.Fatalf("expected ErrRunInvalid, got: %v", err)
	}
}

func TestMutation_FakeProviderSuccessDetection(t *testing.T) {
	oracle := NewEvidenceOracle()

	// Provider makes a claim without any valid evidence references
	unsupportedClaim, err := oracle.RecordClaim(ExecutionClaim{
		ClaimID:      "claim-fake-prov",
		RunID:        "run-fake",
		TaskID:       "task-fake",
		ClaimText:    "Claiming tests passed with no evidence",
		EvidenceRefs: nil,
	})
	if err != nil {
		t.Fatalf("RecordClaim failed: %v", err)
	}
	if unsupportedClaim.Status != ClaimUnsupported {
		t.Fatalf("expected ClaimUnsupported for empty evidence claim, got: %s", unsupportedClaim.Status)
	}

	// Provider makes a claim pointing to non-existent evidence
	staleClaim, err := oracle.RecordClaim(ExecutionClaim{
		ClaimID:      "claim-missing-ev",
		RunID:        "run-fake",
		TaskID:       "task-fake",
		ClaimText:    "Claiming evidence that was never recorded",
		EvidenceRefs: []string{"evidence-does-not-exist"},
	})
	if err != nil {
		t.Fatalf("RecordClaim failed: %v", err)
	}
	if staleClaim.Status != ClaimStale {
		t.Fatalf("expected ClaimStale for missing evidence ref, got: %s", staleClaim.Status)
	}
}

func TestMutation_CorruptedCheckpointDetection(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "app.go")
	_ = os.WriteFile(testFile, []byte("package main\n\nfunc main() {}\n"), 0644)

	cpEngine, err := NewCheckpointEngine(tmpDir)
	if err != nil {
		t.Fatalf("NewCheckpointEngine failed: %v", err)
	}
	ctx := context.Background()

	cp, err := cpEngine.CaptureCheckpoint(ctx, "run-1", "task-1", "original code")
	if err != nil {
		t.Fatalf("CaptureCheckpoint failed: %v", err)
	}

	// Corrupt snapshot directory by replacing with a non-directory file
	_ = os.RemoveAll(cp.WorktreePath)
	_ = os.WriteFile(cp.WorktreePath, []byte("CORRUPTED_NON_DIR_DATA"), 0644)

	// Rollback must detect corrupted archive and fail closed
	_, err = cpEngine.RestoreCheckpoint(ctx, cp.CheckpointID)
	if err == nil {
		t.Fatalf("expected RestoreCheckpoint to fail on corrupted archive, got nil")
	}
}

func TestMutation_CheckpointContentTamperingIsDetectedBeforeRestore(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "app.go")
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	cpEngine, err := NewCheckpointEngine(tmpDir)
	if err != nil {
		t.Fatalf("NewCheckpointEngine failed: %v", err)
	}
	cp, err := cpEngine.CaptureCheckpoint(context.Background(), "run-1", "task-1", "trusted")
	if err != nil {
		t.Fatalf("CaptureCheckpoint failed: %v", err)
	}
	if cp.SnapshotDigest == "" {
		t.Fatal("checkpoint carried no snapshot content digest")
	}

	// Replace bytes inside an otherwise valid snapshot directory. A metadata-
	// only digest would miss this and copy the attacker-controlled content into
	// the live workspace.
	if err := os.WriteFile(filepath.Join(cp.WorktreePath, "app.go"),
		[]byte("package main\n\nfunc main() { panic(\"tampered\") }\n"), 0644); err != nil {
		t.Fatalf("tamper snapshot: %v", err)
	}
	if _, err := cpEngine.RestoreCheckpoint(context.Background(), cp.CheckpointID); err == nil {
		t.Fatal("tampered snapshot content was restored")
	}

	live, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read live source: %v", err)
	}
	if strings.Contains(string(live), "tampered") {
		t.Fatal("tampered bytes reached the live workspace before integrity refusal")
	}
}

func TestAdversarial_CheckpointRestoreRemovesPostCheckpointFiles(t *testing.T) {
	root := t.TempDir()
	baseline := filepath.Join(root, "app.go")
	if err := os.WriteFile(baseline, []byte("trusted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewCheckpointEngine(root)
	if err != nil {
		t.Fatal(err)
	}
	record, err := engine.CaptureCheckpoint(context.Background(), "run-exact", "task-exact", "trusted")
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, "generated", "after-checkpoint.txt")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("must not survive rollback"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RestoreCheckpoint(context.Background(), record.CheckpointID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("post-checkpoint file survived restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".marshal", "checkpoints", record.CheckpointID)); err != nil {
		t.Fatalf("MARSHAL checkpoint metadata was removed: %v", err)
	}
}

func TestAdversarial_CheckpointRecordCannotRedirectRestorePath(t *testing.T) {
	root := t.TempDir()
	livePath := filepath.Join(root, "app.go")
	if err := os.WriteFile(livePath, []byte("trusted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewCheckpointEngine(root)
	if err != nil {
		t.Fatal(err)
	}
	record, err := engine.CaptureCheckpoint(context.Background(), "run-path", "task-path", "trusted")
	if err != nil {
		t.Fatal(err)
	}
	attacker := filepath.Join(root, "attacker-snapshot")
	if err := os.MkdirAll(attacker, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attacker, "app.go"), []byte("attacker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record.WorktreePath = attacker
	record.SnapshotDigest, err = digestSnapshot(attacker)
	if err != nil {
		t.Fatal(err)
	}
	// Even a record whose metadata digest has been recomputed must not redirect
	// restore away from the engine-owned checkpoint directory.
	record.StateDigest = checkpointStateDigest(record)
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".marshal", "checkpoints", record.CheckpointID+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewCheckpointEngine(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RestoreCheckpoint(context.Background(), record.CheckpointID); err == nil {
		t.Fatal("redirected checkpoint path was restored")
	}
	live, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(live) != "trusted\n" {
		t.Fatalf("redirected restore changed live content to %q", live)
	}
}

func TestAdversarial_RollbackRefusesWhileRunIsActive(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewMemoryRunStore()
	engine, err := NewEngine(EngineConfig{ProjectRoot: tmpDir, MaxWorkers: 1}, store, nil)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	goal, executionPlan := createTestGoalAndPlan(now)
	handoff := createTestHandoff(t, tmpDir, goal, executionPlan)
	run, err := engine.InitializeRun(ctx, handoff, goal, executionPlan)
	if err != nil {
		t.Fatalf("InitializeRun failed: %v", err)
	}
	cp, err := engine.CaptureCheckpoint(ctx, run.RunID, "baseline", "before active work")
	if err != nil {
		t.Fatalf("CaptureCheckpoint failed: %v", err)
	}

	run.State = RunRunning
	run.CurrentPhase = PhaseExecuting
	for id, task := range run.Tasks {
		task.State = TaskRunning
		run.Tasks[id] = task
		break
	}
	if err := store.UpdateRun(ctx, *run); err != nil {
		t.Fatalf("mark run active: %v", err)
	}

	if _, err := engine.RollbackToCheckpoint(ctx, cp.CheckpointID); err == nil {
		t.Fatal("rollback proceeded while canonical run state was active")
	}
}

// =========================================================================
// File 45: Chaos & Crash Resilience Matrix
// =========================================================================

func TestChaos_ProcessExitUnexpectedly(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := EngineConfig{
		ProjectRoot: tmpDir,
		MaxWorkers:  1,
		DefaultTTL:  1 * time.Minute,
	}
	store := NewMemoryRunStore()
	engine, err := NewEngine(cfg, store, nil)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	ctx := context.Background()

	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)
	handoff := createTestHandoff(t, tmpDir, goal, p)

	run, err := engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		t.Fatalf("InitializeRun failed: %v", err)
	}

	// Register a mock harness that simulates an unexpected process exit (exit 137 / SIGKILL)
	engine.RegisterHarness(NewMockHarness("mock", func(ctx context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error) {
		return TaskResult{
			TaskID:       task.TaskID,
			Success:      false,
			ErrorMessage: "process received SIGKILL / out of memory",
		}, fmt.Errorf("process terminated unexpectedly with code 137")
	}))

	executedRun, err := engine.ExecuteRun(ctx, run.RunID)
	if err == nil {
		t.Fatalf("expected ExecuteRun to fail due to process crash, got nil")
	}

	// Inspect run state: must be marked Failed, no state corruption
	if executedRun.State != RunFailed {
		t.Fatalf("expected status Failed, got: %s", executedRun.State)
	}
	if executedRun.Tasks["task-1"].State != TaskFailed {
		t.Fatalf("expected task state Failed, got: %s", executedRun.Tasks["task-1"].State)
	}
}

func TestChaos_CrashAndIdempotentResume(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := EngineConfig{
		ProjectRoot: tmpDir,
		MaxWorkers:  1,
		DefaultTTL:  1 * time.Minute,
	}
	store := NewMemoryRunStore()
	engine, err := NewEngine(cfg, store, nil)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	ctx := context.Background()

	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)
	handoff := createTestHandoff(t, tmpDir, goal, p)

	run, err := engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		t.Fatalf("InitializeRun failed: %v", err)
	}

	var task1Executed int
	var task2Executed int

	// First pass: task-1 succeeds, task-2 fails with transient interruption
	engine.RegisterHarness(NewMockHarness("mock", func(ctx context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error) {
		if task.TaskID == "task-1" {
			task1Executed++
			return TaskResult{
				TaskID:  task.TaskID,
				Success: true,
				Claims: []ExecutionClaim{
					{ClaimID: "c1", TaskID: task.TaskID, ClaimText: "done", Status: ClaimSupported, EvidenceRefs: []string{"ev1"}},
				},
			}, nil
		}
		if task.TaskID == "task-2" {
			task2Executed++
			return TaskResult{
				TaskID:       task.TaskID,
				Success:      false,
				ErrorMessage: "simulated mid-run crash",
			}, errors.New("mid-run crash")
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))

	_, _ = engine.ExecuteRun(ctx, run.RunID)

	if task1Executed != 1 {
		t.Fatalf("expected task 1 executed 1 time, got %d", task1Executed)
	}

	// Verify task 1 succeeded and task 2 failed in store
	loaded1, _ := store.GetRun(ctx, run.RunID)
	if loaded1.Tasks["task-1"].State != TaskCompletedPendingVerify {
		t.Fatalf("expected task 1 completed, got: %s", loaded1.Tasks["task-1"].State)
	}

	// Now simulate engine restart: create new Engine instance sharing the same store
	engine2, err := NewEngine(cfg, store, nil)
	if err != nil {
		t.Fatalf("NewEngine 2 failed: %v", err)
	}
	engine2.SetRunContext(run.RunID, goal, p)

	// Reset task 2 to Pending and run to Ready for resume
	t2 := loaded1.Tasks["task-2"]
	t2.State = TaskPending
	loaded1.Tasks["task-2"] = t2
	loaded1.State = RunReady
	_ = store.UpdateRun(ctx, loaded1)

	// Second pass: mock harness now allows task 2 and task 3 to succeed
	engine2.RegisterHarness(NewMockHarness("mock", func(ctx context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error) {
		if task.TaskID == "task-1" {
			task1Executed++
		}
		if task.TaskID == "task-2" {
			task2Executed++
		}
		return TaskResult{
			TaskID:  task.TaskID,
			Success: true,
			Claims: []ExecutionClaim{
				{ClaimID: "c-" + task.TaskID, TaskID: task.TaskID, ClaimText: "done", Status: ClaimSupported, EvidenceRefs: []string{"ev-" + task.TaskID}},
			},
		}, nil
	}))

	resumedRun, err := engine2.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("resumed ExecuteRun failed: %v", err)
	}

	if resumedRun.State != RunDonePendingVerification {
		t.Fatalf("expected resumed run in RunDonePendingVerification, got %s", resumedRun.State)
	}

	// Task 1 should NOT have re-executed (idempotent resume!)
	if task1Executed != 1 {
		t.Fatalf("expected task 1 to NOT re-execute, got count: %d", task1Executed)
	}
	// Task 2 should have executed a second time and succeeded
	if task2Executed != 2 {
		t.Fatalf("expected task 2 to execute twice (1 fail + 1 succeed), got: %d", task2Executed)
	}
}

// =========================================================================
// File 46: Scale & Stress Tests
// =========================================================================

func TestScale_100TasksDAGResolution(t *testing.T) {
	tasks := make(map[string]TaskExecution)
	const layers = 10
	const perLayer = 10

	for l := 0; l < layers; l++ {
		for i := 0; i < perLayer; i++ {
			id := fmt.Sprintf("t-%d-%d", l, i)
			var deps []string
			if l > 0 {
				for prev := 0; prev < perLayer; prev++ {
					deps = append(deps, fmt.Sprintf("t-%d-%d", l-1, prev))
				}
			}
			tasks[id] = TaskExecution{
				TaskID:       id,
				State:        TaskPending,
				Dependencies: deps,
			}
		}
	}

	run := &ExecutionRun{
		RunID: "scale-run",
		Tasks: tasks,
	}
	lm := NewLeaseManager()
	scheduler := NewScheduler(8, lm)

	// Simulate layered DAG resolution
	now := time.Now().UTC()
	resolvedCount := 0
	for l := 0; l < layers; l++ {
		readyIDs, err := scheduler.RefreshTaskReadiness(run, now)
		if err != nil {
			t.Fatalf("layer %d refresh failed: %v", l, err)
		}
		if len(readyIDs) != perLayer {
			t.Fatalf("layer %d: expected %d ready tasks, got %d", l, perLayer, len(readyIDs))
		}
		for _, id := range readyIDs {
			task := run.Tasks[id]
			task.State = TaskCompletedPendingVerify
			run.Tasks[id] = task
			resolvedCount++
		}
	}

	if resolvedCount != layers*perLayer {
		t.Fatalf("expected %d total resolved tasks, got %d", layers*perLayer, resolvedCount)
	}
}

func TestScale_10000JournalEventsHashChaining(t *testing.T) {
	journal := NewMemoryJournalStore()
	start := time.Now()
	const eventCount = 10000
	runID := "run-scale-10k"

	for i := 0; i < eventCount; i++ {
		_, err := journal.Append(JournalEvent{
			RunID:       runID,
			TaskID:      fmt.Sprintf("task-%d", i%50),
			Timestamp:   time.Now().UTC(),
			Actor:       "worker-scale",
			EventType:   "heartbeat",
			Summary:     fmt.Sprintf("Event sequence %d", i),
			PayloadJSON: fmt.Sprintf(`{"seq":%d}`, i),
		})
		if err != nil {
			t.Fatalf("failed to append event %d: %v", i, err)
		}
	}
	appendDuration := time.Since(start)
	t.Logf("Appended %d hash-chained events in %v (%.2f ev/s)", eventCount, appendDuration, float64(eventCount)/appendDuration.Seconds())

	// Verify the entire chain
	verifyStart := time.Now()
	err := journal.VerifyIntegrity(runID)
	if err != nil {
		t.Fatalf("VerifyIntegrity failed: %v", err)
	}
	verifyDuration := time.Since(verifyStart)
	t.Logf("Verified 10,000 event hash chain in %v", verifyDuration)

	if verifyDuration > 2*time.Second {
		t.Errorf("chain verification exceeded SLA: %v", verifyDuration)
	}
}

func TestScale_LargeToolOutputBounding(t *testing.T) {
	// Generate 5MB of tool output with mixed ANSI codes and lines
	var buf bytes.Buffer
	for buf.Len() < 5*1024*1024 {
		buf.WriteString("\x1b[32m[INFO]\x1b[0m Standard output line with text data.\n")
	}

	bounded := StripAndBoundLogs(buf.String(), MaxToolOutputBytes)
	if len(bounded) > MaxToolOutputBytes+200 { // +200 allowance for truncation note
		t.Errorf("bounded output exceeded MaxToolOutputBytes: got %d bytes", len(bounded))
	}

	// Verify ANSI escapes were stripped
	if bytes.Contains([]byte(bounded), []byte("\x1b[32m")) {
		t.Errorf("bounded output still contains raw ANSI escape sequences")
	}
}

// =========================================================================
// File 47: Rate Limit & Quota Runtime
// =========================================================================

func TestRateLimit_RetryAfterParsing(t *testing.T) {
	now := time.Now()

	// Integer seconds
	d, err := ParseRetryAfter("45", now)
	if err != nil {
		t.Fatalf("ParseRetryAfter int failed: %v", err)
	}
	if d != 45*time.Second {
		t.Errorf("expected 45s, got: %v", d)
	}

	// Negative value rejected
	_, err = ParseRetryAfter("-5", now)
	if err == nil {
		t.Errorf("expected error for negative seconds, got nil")
	}

	// HTTP-date (RFC1123)
	futureDate := now.Add(90 * time.Second).UTC().Format(http.TimeFormat)
	d2, err := ParseRetryAfter(futureDate, now)
	if err != nil {
		t.Fatalf("ParseRetryAfter date failed: %v", err)
	}
	if d2 < 85*time.Second || d2 > 95*time.Second {
		t.Errorf("expected ~90s duration, got: %v", d2)
	}

	// Past date returns 0 duration without error
	pastDate := now.Add(-60 * time.Second).UTC().Format(http.TimeFormat)
	d3, err := ParseRetryAfter(pastDate, now)
	if err != nil {
		t.Fatalf("ParseRetryAfter past date failed: %v", err)
	}
	if d3 != 0 {
		t.Errorf("expected 0 duration for past date, got: %v", d3)
	}
}

func TestRateLimit_BackoffCalculation(t *testing.T) {
	calc := DefaultBackoffCalculator()

	// Zero attempt
	d0 := calc.CalculateDelay(0, 0)
	if d0 < 350*time.Millisecond || d0 > 650*time.Millisecond {
		t.Errorf("attempt 0 unexpected delay: %v", d0)
	}

	// Attempt 3 with Retry-After floor of 10s
	d3 := calc.CalculateDelay(3, 10*time.Second)
	if d3 < 10*time.Second {
		t.Errorf("expected delay to respect Retry-After floor of 10s, got: %v", d3)
	}

	// Attempt 100 capped at MaxDelay
	dMax := calc.CalculateDelay(100, 0)
	if dMax > calc.MaxDelay*12/10 { // allowing jitter
		t.Errorf("expected delay capped near MaxDelay %v, got: %v", calc.MaxDelay, dMax)
	}
}

func TestRateLimit_TrackerAndFallbackWorker(t *testing.T) {
	tracker := NewRateLimitTracker()
	now := time.Now()

	task := &TaskExecution{
		TaskID:            "task-quota",
		AssignedHarness:   "claude",
		AssignedModel:     "claude-3-5-sonnet",
		FallbackHarnesses: []string{"codex", "opencode"},
	}

	// Initially primary is available
	selected, ok := tracker.SelectAvailableWorker(task, now)
	if !ok || selected != "claude" {
		t.Fatalf("expected primary worker claude, got: %s (ok=%v)", selected, ok)
	}

	// Record rate limit for claude
	tracker.RecordRateLimit(RateLimitRecord{
		Harness:    "claude",
		Model:      "claude-3-5-sonnet",
		HTTPStatus: 429,
		RetryAfter: 30 * time.Second,
		ObservedAt: now,
		Source:     "provider_header",
	})

	// Now tracker should detect claude is throttled and select fallback "codex"
	selectedFallback, ok := tracker.SelectAvailableWorker(task, now)
	if !ok || selectedFallback != "codex" {
		t.Fatalf("expected fallback worker codex, got: %s (ok=%v)", selectedFallback, ok)
	}

	// Also throttle codex
	tracker.RecordRateLimit(RateLimitRecord{
		Harness:    "codex",
		HTTPStatus: 429,
		RetryAfter: 60 * time.Second,
		ObservedAt: now,
		Source:     "provider_header",
	})

	// Now tracker should select secondary fallback "opencode"
	selectedFallback2, ok := tracker.SelectAvailableWorker(task, now)
	if !ok || selectedFallback2 != "opencode" {
		t.Fatalf("expected fallback worker opencode, got: %s (ok=%v)", selectedFallback2, ok)
	}

	// Throttle opencode as well
	tracker.RecordRateLimit(RateLimitRecord{
		Harness:    "opencode",
		HTTPStatus: 429,
		RetryAfter: 60 * time.Second,
		ObservedAt: now,
		Source:     "provider_header",
	})

	// All workers throttled: must report false
	_, ok = tracker.SelectAvailableWorker(task, now)
	if ok {
		t.Fatalf("expected all workers to be throttled, got ok=true")
	}

	// After cooldown expiry, claude should be available again
	future := now.Add(35 * time.Second)
	selectedAfterCooldown, ok := tracker.SelectAvailableWorker(task, future)
	if !ok || selectedAfterCooldown != "claude" {
		t.Fatalf("expected claude available after cooldown, got: %s (ok=%v)", selectedAfterCooldown, ok)
	}
}
