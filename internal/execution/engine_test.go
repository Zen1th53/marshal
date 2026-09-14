package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

const testProjectID = projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")

func createTestHandoff(t *testing.T, projectRoot string, goal model.GoalContract, p plan.ExecutionPlan) plan.Handoff {
	t.Helper()
	now := time.Now().UTC()
	handoff, err := plan.PrepareHandoff(p, goal, testProjectID, now)
	if err != nil {
		t.Fatalf("PrepareHandoff failed: %v", err)
	}
	return handoff
}

func createTestGoalAndPlan(now time.Time) (model.GoalContract, plan.ExecutionPlan) {
	goal := model.GoalContract{
		ID:                  "GOAL-001",
		SessionID:           "SESSION-001",
		Revision:            1,
		ProjectID:           string(testProjectID),
		OriginalRequest:     "Implement security feature",
		RequestDigest:       "sha256:abc",
		ConstitutionVersion: constitution.Current.String(),
		Confirmation:        model.ConfirmationApproved,
		DesiredOutcome:      "Security feature is fully implemented and tested",
		SuccessCriteria:     []string{"policy applied", "tests pass"},
		Risk:                model.R2,
		AuthoritySource:     "operator",
		Constraints: []model.Constraint{
			{
				ID:     "C-01",
				Text:   "Do not touch production database",
				IsHard: true,
			},
		},
	}

	capacity := goalintake.UnknownCapacity("codex", true)
	candidates := []goalintake.Candidate{
		{Provider: "codex", Model: "m", Governance: constitution.GovernanceVerified, Capacity: capacity},
		{Provider: "claude", Model: "m", Governance: constitution.GovernanceVerified, Capacity: goalintake.UnknownCapacity("claude", true)},
	}
	harnessCandidates := []plan.HarnessCandidate{{
		Profile:          model.HarnessProfile{Harness: "mock", InstalledVersion: "1.0.0", SupportedModels: []string{"m"}, DefaultModel: "m", FeatureSupport: map[string]model.FeatureStatus{"code_edit": model.StatusNative}, ProbeEvidenceID: "EVIDENCE-plan", ProbedAt: now},
		InstalledVersion: "1.0.0", Provider: "codex", Capacity: capacity,
	}}

	tasks := []plan.Task{
		{
			ID:       "task-1",
			Title:    "Initialize configuration",
			Mutating: false,
			Paths:    []string{"config.json"},
		},
		{
			ID:        "task-2",
			Title:     "Apply security policy",
			Mutating:  true,
			Paths:     []string{"policy.go"},
			Criteria:  []string{"policy applied"},
			DependsOn: []string{"task-1"},
		},
		{
			ID:        "task-3",
			Title:     "Run verification tests",
			Mutating:  false,
			Paths:     []string{"policy_test.go"},
			Criteria:  []string{"tests pass"},
			DependsOn: []string{"task-2"},
		},
	}

	assessment := goalintake.AssessRequest("Implement security feature", goalintake.RequestContext{Recoverable: true, ScopeKnown: true})

	built, err := plan.Build(plan.BuildRequest{
		Goal:       goal,
		ProjectID:  testProjectID,
		Assessment: assessment,
		Tasks:      tasks,
		Mode:       plan.ModeStandard,
		Version:    constitution.Current,
		Candidates: candidates,
		Scope:      []string{"."},
		Now:        now,
	})
	if err != nil {
		panic(err)
	}

	built.Assignments = plan.AssignHarnesses(plan.AssignRequest{
		Team:                   built.Team,
		Tasks:                  built.Tasks,
		Assessment:             built.Assessment,
		Candidates:             harnessCandidates,
		RequiredCapabilities:   []string{"code_edit"},
		EstimatedContextTokens: 1000,
		Now:                    now,
	})

	approved, err := built.Approve(now)
	if err != nil {
		panic(err)
	}

	return goal, approved
}

func TestEngine_InitializeRun_EntryGateEnforcement(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	cfg := EngineConfig{
		ProjectRoot: tmpDir,
		MaxWorkers:  2,
		DefaultTTL:  1 * time.Minute,
	}
	pReader := NewMemoryPlanReader()
	gReader := NewMemoryGoalReader()
	engine, err := NewEngineWithReaders(cfg, nil, nil, pReader, gReader)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)

	// Case 1: Valid approved handoff
	handoff := createTestHandoff(t, tmpDir, goal, p)
	run, err := engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		t.Fatalf("expected successful initialization, got: %v", err)
	}
	if run == nil || run.State != RunReady {
		t.Fatalf("expected run in READY state, got %+v", run)
	}
	if len(run.HardConstraints) != 1 || run.HardConstraints[0] != "Do not touch production database" {
		t.Fatalf("expected hard constraint re-injected, got %+v", run.HardConstraints)
	}

	// Case 2: Unapproved plan must be BLOCKED
	unapprovedPlan := p
	unapprovedPlan.ID = "PLAN-UNAPPROVED"
	unapprovedPlan.State = plan.StateDraft
	unapprovedHandoff := handoff
	unapprovedHandoff.PlanID = unapprovedPlan.ID
	pReader.AddPlan(unapprovedPlan)

	_, err = engine.InitializeRun(ctx, unapprovedHandoff, goal, unapprovedPlan)
	if err == nil {
		t.Fatal("expected EntryGate to block unapproved plan, but got success")
	}

	// Case 3: Changed Goal revision must be BLOCKED
	movedGoal := goal
	movedGoal.Revision = 2
	gReader.AddGoal(movedGoal)
	staleHandoff := handoff
	_, err = engine.InitializeRun(ctx, staleHandoff, movedGoal, p)
	if err == nil {
		t.Fatal("expected EntryGate to block moved goal revision, but got success")
	}
}

func TestNewRunFromHandoffBindsEveryTaskToOwningRun(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	goal, p := createTestGoalAndPlan(now)
	handoff := createTestHandoff(t, t.TempDir(), goal, p)
	run, err := NewRunFromHandoff(handoff, now, RunProvenance{RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID == "" {
		t.Fatal("run has no durable identity")
	}
	for id, task := range run.Tasks {
		if task.RunID != run.RunID {
			t.Fatalf("task %s run binding = %q, want %q", id, task.RunID, run.RunID)
		}
	}
}

func TestEngine_ExecuteRun_DAGOrderingAndLifecycle(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	cfg := EngineConfig{
		ProjectRoot: tmpDir,
		MaxWorkers:  2,
		DefaultTTL:  1 * time.Minute,
	}
	engine, err := NewEngine(cfg, nil, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	var executedTasks []string
	mockHarness := NewMockHarness("mock", func(ctx context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error) {
		executedTasks = append(executedTasks, task.TaskID)
		return TaskResult{
			TaskID:  task.TaskID,
			Success: true,
			Claims: []ExecutionClaim{
				{
					ClaimID:      "claim-" + task.TaskID,
					TaskID:       task.TaskID,
					ClaimText:    "Task finished cleanly",
					Status:       ClaimSupported,
					EvidenceRefs: []string{"ev-" + task.TaskID},
				},
			},
		}, nil
	})
	engine.RegisterHarness(mockHarness)

	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)
	handoff := createTestHandoff(t, tmpDir, goal, p)

	run, err := engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		t.Fatalf("InitializeRun failed: %v", err)
	}

	executedRun, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("ExecuteRun failed: %v", err)
	}

	if executedRun.State != RunDonePendingVerification {
		t.Fatalf("expected RunDonePendingVerification, got %s", executedRun.State)
	}

	// Verify tasks were executed
	if len(executedTasks) != 3 {
		t.Fatalf("expected 3 tasks executed, got %d: %v", len(executedTasks), executedTasks)
	}

	// Verify journal contains the full lifecycle events
	events, err := engine.journal.GetEvents(run.RunID)
	if err != nil {
		t.Fatalf("failed to get journal events: %v", err)
	}
	if len(events) < 5 {
		t.Fatalf("expected at least 5 journal events, got %d", len(events))
	}
}

func TestEngine_RuntimeApprovalFlow(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	cfg := EngineConfig{
		ProjectRoot: tmpDir,
		MaxWorkers:  2,
		DefaultTTL:  1 * time.Minute,
	}
	engine, err := NewEngine(cfg, nil, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)

	handoff := createTestHandoff(t, tmpDir, goal, p)
	run, err := engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		t.Fatalf("InitializeRun failed: %v", err)
	}

	// Manually require approval on task-2 in run
	t2 := run.Tasks["task-2"]
	t2.ApprovalRequired = true
	run.Tasks["task-2"] = t2
	if err := engine.store.UpdateRun(ctx, *run); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}

	// First execution step: task-1 runs, task-2 requires approval
	execRun, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("ExecuteRun step 1 failed: %v", err)
	}

	if execRun.State != RunNeedsApproval {
		t.Fatalf("expected RunNeedsApproval, got %s", execRun.State)
	}
	task2 := execRun.Tasks["task-2"]
	if task2.State != TaskNeedsApproval || task2.ApprovalID == "" {
		t.Fatalf("expected task-2 in TaskNeedsApproval with ApprovalID, got %+v", task2)
	}

	// User approves task-2
	err = engine.DecideApproval(ctx, task2.ApprovalID, true, "lead_operator", "Approved after review")
	if err != nil {
		t.Fatalf("DecideApproval failed: %v", err)
	}

	// Second execution step: task-2 executes, then task-3 completes the run
	resumedRun, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("ExecuteRun step 2 failed: %v", err)
	}

	if resumedRun.State != RunDonePendingVerification {
		t.Fatalf("expected RunDonePendingVerification after approval, got %s", resumedRun.State)
	}
}

func TestEngine_ProviderNativeApprovalPausesAndResumesThroughCanonicalDecision(t *testing.T) {
	ctx := context.Background()
	engine, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir(), MaxWorkers: 1, DefaultTTL: time.Minute}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var taskTwoAttempts int
	engine.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		if task.TaskID != "task-2" {
			return TaskResult{TaskID: task.TaskID, Success: true}, nil
		}
		taskTwoAttempts++
		if taskTwoAttempts == 1 {
			return TaskResult{TaskID: task.TaskID, NeedsApproval: true, ApprovalReq: &ApprovalRequest{
				OperationType: "PROVIDER_TOOL_NATIVE", TargetResource: "policy.go", Scope: "thread=one turn=one item=one digest=sha256:one",
				DiffPreview: "item/commandExecution/requestApproval", Parameters: "sha256:one", CurrentState: "thread=one turn=one item=one",
			}}, nil
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)
	handoff := createTestHandoff(t, engine.cfg.ProjectRoot, goal, p)
	run, err := engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("ExecuteRun pause: %v", err)
	}
	two := paused.Tasks["task-2"]
	if paused.State != RunNeedsApproval || two.State != TaskNeedsApproval || two.ApprovalID == "" {
		t.Fatalf("provider approval must pause canonical Process 05: run=%s task=%+v", paused.State, two)
	}
	record, err := engine.ApprovalManager().GetApproval(two.ApprovalID)
	if err != nil || record.RunID != run.RunID || record.TaskID != two.TaskID || record.OperationType != "PROVIDER_TOOL_NATIVE" {
		t.Fatalf("native approval binding = %#v, %v", record, err)
	}
	if err := engine.DecideApproval(ctx, two.ApprovalID, true, "operator", "reviewed exact native request"); err != nil {
		t.Fatal(err)
	}
	resumed, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("ExecuteRun resume: %v", err)
	}
	if resumed.State != RunDonePendingVerification || taskTwoAttempts != 2 {
		t.Fatalf("approved task must be re-admitted exactly once: run=%s attempts=%d", resumed.State, taskTwoAttempts)
	}
}

func TestEngine_LiveNativeTurnPersistsExactBindingAndResumesOnlyAfterConsumption(t *testing.T) {
	ctx := context.Background()
	engine, err := NewEngine(EngineConfig{ProjectRoot: t.TempDir(), MaxWorkers: 1, DefaultTTL: time.Minute}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var attempts int
	var approvalID string
	engine.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error) {
		if task.TaskID != "task-2" {
			return TaskResult{TaskID: task.TaskID, Success: true}, nil
		}
		attempts++
		if attempts == 1 {
			record, err := engine.ApprovalManager().RequestApproval(ApprovalRequest{
				RunID: task.RunID, TaskID: task.TaskID, OperationType: "CODEX_APP_SERVER_NATIVE", TargetResource: worktree,
				Scope: "thread=t turn=u item=v digest=sha256:request", DiffPreview: "item/commandExecution/requestApproval", Parameters: "sha256:request",
				CurrentState: "bound native request", Now: time.Now().UTC(),
			})
			if err != nil {
				return TaskResult{}, err
			}
			approvalID = record.ApprovalID
			return TaskResult{TaskID: task.TaskID, NeedsApproval: true, NativeApprovalID: record.ApprovalID,
				ApprovalReq: &ApprovalRequest{OperationType: "CODEX_APP_SERVER_NATIVE", TargetResource: worktree, Scope: "thread=t turn=u item=v digest=sha256:request", CurrentState: "bound native request"},
				NativeTurn:  &NativeTurnBinding{Provider: "codex-app-server", ThreadID: "t", TurnID: "u", Worktree: worktree, WorktreeDigest: "test-worktree", ConstraintDigest: pkg.Digest, RunRevision: task.RunRevision, State: "APPROVAL_REQUIRED"}}, nil
		}
		binding := *task.NativeTurn
		binding.RunRevision = task.RunRevision
		return TaskResult{TaskID: task.TaskID, Success: true, NativeTurn: &binding}, nil
	}))
	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)
	handoff := createTestHandoff(t, engine.cfg.ProjectRoot, goal, p)
	run, err := engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	task := paused.Tasks["task-2"]
	if paused.State != RunNeedsApproval || task.NativeTurn == nil || task.NativeTurn.ThreadID != "t" || task.NativeTurn.TurnID != "u" || task.ApprovalID != approvalID {
		t.Fatalf("native pause did not persist exact binding: run=%s task=%+v", paused.State, task)
	}
	stored, err := engine.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Tasks["task-2"].NativeTurn == nil || stored.Tasks["task-2"].NativeTurn.RunRevision != stored.Version {
		t.Fatalf("durable binding must be tied to persisted post-pause revision: task=%+v runVersion=%d", stored.Tasks["task-2"], stored.Version)
	}
	// An ordinary approval cannot release a live native turn.
	if err := engine.DecideApproval(ctx, approvalID, true, "operator", "attempt bypass"); !errors.Is(err, ErrRunBlocked) {
		t.Fatalf("ordinary native decision = %v, want blocked", err)
	}
	if err := engine.ApprovalManager().Approve(approvalID, "operator", "reviewed", now); err != nil {
		t.Fatal(err)
	}
	if err := engine.ApprovalManager().ValidateAndConsume(approvalID,
		ComputeActionDigest("CODEX_APP_SERVER_NATIVE", stored.Tasks["task-2"].WorktreePath, "item/commandExecution/requestApproval", "sha256:request"),
		ComputeStateDigest("bound native request"), now); err != nil {
		t.Fatal(err)
	}
	if err := engine.ResumeNativeApproval(ctx, approvalID); err != nil {
		t.Fatalf("resume consumed native approval: %v", err)
	}
	resumed, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || resumed.State != RunDonePendingVerification {
		t.Fatalf("native turn did not resume exactly once: attempts=%d state=%s", attempts, resumed.State)
	}
}

func TestEngine_Process06HandoffBundleAssembly(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	cfg := EngineConfig{
		ProjectRoot: tmpDir,
		MaxWorkers:  1,
		DefaultTTL:  1 * time.Minute,
	}
	engine, err := NewEngine(cfg, nil, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)
	handoff := createTestHandoff(t, tmpDir, goal, p)

	run, err := engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		t.Fatalf("InitializeRun failed: %v", err)
	}

	completedRun, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("ExecuteRun failed: %v", err)
	}

	bundle, err := engine.AssembleProcess06Bundle(ctx, completedRun.RunID)
	if err != nil {
		t.Fatalf("AssembleProcess06Bundle failed: %v", err)
	}

	if bundle.RunID != completedRun.RunID {
		t.Fatalf("bundle RunID = %s, want %s", bundle.RunID, completedRun.RunID)
	}
	if bundle.PlanID != p.ID || bundle.GoalID != goal.ID {
		t.Fatalf("bundle mismatched PlanID=%s or GoalID=%s", bundle.PlanID, bundle.GoalID)
	}
	if len(bundle.Tasks) != 3 {
		t.Fatalf("expected 3 tasks in bundle, got %d", len(bundle.Tasks))
	}
	if bundle.FinalState != RunDonePendingVerification {
		t.Fatalf("expected final state RunDonePendingVerification, got %s", bundle.FinalState)
	}
}

func TestEngine_CheckpointAndRollback(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(testFile, []byte("original content"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cpEngine, err := NewCheckpointEngine(tmpDir)
	if err != nil {
		t.Fatalf("NewCheckpointEngine: %v", err)
	}

	// Capture initial checkpoint
	cp, err := cpEngine.CaptureCheckpoint(ctx, "run-1", "task-1", "before mutation")
	if err != nil {
		t.Fatalf("CaptureCheckpoint: %v", err)
	}

	// Mutate file
	if err := os.WriteFile(testFile, []byte("corrupted or broken mutation"), 0644); err != nil {
		t.Fatalf("mutate file: %v", err)
	}

	// Restore checkpoint
	_, err = cpEngine.RestoreCheckpoint(ctx, cp.CheckpointID)
	if err != nil {
		t.Fatalf("RestoreCheckpoint: %v", err)
	}

	restored, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(restored) != "original content" {
		t.Fatalf("expected 'original content', got %q", string(restored))
	}
}
