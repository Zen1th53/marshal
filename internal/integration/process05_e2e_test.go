package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func gitCommitAll(t *testing.T, dir, msg string) {
	t.Helper()
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	cmd = exec.Command("git", "commit", "-m", msg)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
}

func TestProcess05_FullChainE2E(t *testing.T) {
	ctx := context.Background()
	repo := runtimeIntegrationRepo(t)
	projectDir := repo.Path()

	if _, err := app.Bootstrap(ctx, projectDir); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	runtime, err := app.Open(ctx, projectDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer runtime.Close()

	// Initial repository file
	readmeFile := filepath.Join(projectDir, "README.md")
	if err := os.WriteFile(readmeFile, []byte("# Initial System\n"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	gitCommitAll(t, projectDir, "initial readme commit")

	// Phase 1: Natural language request -> Process 03 Goal Contract
	originalReq := "Safely refactor logging and update docs, but do not touch security package."
	sessionID := "SESSION-P05-E2E"
	projectID := projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")

	goal := model.GoalContract{
		ID:                  "GOAL-P05-001",
		SessionID:           sessionID,
		Revision:            1,
		ProjectID:           string(projectID),
		OriginalRequest:     originalReq,
		ConstitutionVersion: constitution.Current.String(),
		Confirmation:        model.ConfirmationApproved,
		DesiredOutcome:      "Logging is refactored and docs are updated",
		ExpectedArtifact:    "docs/logging.md",
		Constraints: []model.Constraint{
			{ID: "c1", Text: "Do not touch security package", IsHard: true},
			{ID: "c2", Text: "Preserve backward compatibility", IsHard: true},
		},
		DoNotDo:         []string{"modify internal/security"},
		SuccessCriteria: []string{"logging refactored", "docs/logging.md created"},
		Risk:            model.R1,
		AuthoritySource: "operator",
	}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("SaveGoalContract: %v", err)
	}

	// Phase 2: Process 04 Plan / DAG / Team / Verification / Approvals
	now := time.Now().UTC()
	cap := goalintake.UnknownCapacity("openai", true)
	createReq := app.CreatePlanRequest{
		SessionID: sessionID,
		ProjectID: projectID,
		Tasks: []plan.Task{
			{
				ID:       "task-1",
				Title:    "refactor logging subsystem",
				Mutating: true,
				Paths:    []string{"pkg/log/log.go"},
				Criteria: []string{"logging refactored"},
			},
			{
				ID:        "task-2",
				Title:     "document logging subsystem",
				Mutating:  true,
				Paths:     []string{"docs/logging.md"},
				Criteria:  []string{"docs/logging.md created"},
				DependsOn: []string{"task-1"},
			},
		},
		Candidates: []goalintake.Candidate{
			{Provider: "openai", Model: "gpt-4o", Capacity: cap, Governance: constitution.GovernanceVerified},
		},
		HarnessCandidates: []plan.HarnessCandidate{
			{
				Profile: model.HarnessProfile{
					Harness:         "mock-harness",
					InstalledVersion: "1.0.0",
					SupportedModels: []string{"gpt-4o"},
					DefaultModel:    "gpt-4o",
					ProbeEvidenceID: "EV-mock",
					ProbedAt:        now,
				},
				InstalledVersion: "1.0.0",
				Provider:         "openai",
				Capacity:         cap,
			},
		},
	}

	_, err = runtime.Plans().Create(ctx, createReq)
	if err != nil {
		t.Fatalf("Plans.Create: %v", err)
	}
	approvedPlan, err := runtime.Plans().Approve(ctx, projectID)
	if err != nil {
		t.Fatalf("Plans.Approve: %v", err)
	}

	// Phase 3: Process 05 Governed Execution
	// 1. Register Worker Harness
	var executedTasks []string
	var receivedPackages []execution.ConstraintPackage

	runtime.Execution().RegisterHarness(execution.NewMockHarness("openai", func(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		executedTasks = append(executedTasks, task.TaskID)
		receivedPackages = append(receivedPackages, pkg)

		// Create target files in worktree
		for _, p := range task.TargetFiles {
			absPath := filepath.Join(worktree, p)
			_ = os.MkdirAll(filepath.Dir(absPath), 0755)
			_ = os.WriteFile(absPath, []byte("// Generated content for "+task.TaskID+"\n"), 0644)
		}

		return execution.TaskResult{
			TaskID:  task.TaskID,
			Success: true,
			Claims: []execution.ExecutionClaim{
				{
					ClaimID:      "claim-" + task.TaskID,
					TaskID:       task.TaskID,
					ClaimText:    "Completed " + task.Description,
					Status:       execution.ClaimSupported,
					EvidenceRefs: []string{"ev-" + task.TaskID},
				},
			},
			EvidenceList: []execution.ExecutionEvidence{
				{
					EvidenceID:   "ev-" + task.TaskID,
					TaskID:       task.TaskID,
					ToolName:     "tool_write",
					OutputDigest: "digest-" + task.TaskID,
					CreatedAt:    time.Now().UTC(),
					Status:       execution.EvidenceValid,
				},
			},
		}, nil
	}))

	// 2. Start Run from Process 04 handoff
	run, err := runtime.Execution().StartRun(ctx, sessionID, projectID)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// PROVE: Original request preserved
	if run.OriginalRequest != originalReq {
		t.Fatalf("original request mismatch: got %q, want %q", run.OriginalRequest, originalReq)
	}

	// PROVE: Hard constraints preserved
	foundConstraint := false
	for _, hc := range run.HardConstraints {
		if strings.Contains(hc, "Do not touch security package") {
			foundConstraint = true
			break
		}
	}
	if !foundConstraint {
		t.Fatalf("hard constraint 'Do not touch security package' was lost in run: %v", run.HardConstraints)
	}

	// PROVE: Goal and Plan versions exact
	if run.GoalRevision != goal.Revision {
		t.Fatalf("goal revision mismatch: got %d, want %d", run.GoalRevision, goal.Revision)
	}
	if run.PlanVersion != approvedPlan.Version {
		t.Fatalf("plan version mismatch: got %d, want %d", run.PlanVersion, approvedPlan.Version)
	}

	// PROVE: Baseline checkpoint captured
	if len(run.Checkpoints) == 0 {
		t.Fatalf("expected baseline checkpoint to be captured at run start")
	}

	// 3. Execute Run - Phase 1 (Approval Gating)
	execRun, err := runtime.Execution().ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("ExecuteRun: %v", err)
	}

	for execRun != nil && execRun.State == execution.RunNeedsApproval {
		var pendingAppID string
		for _, taskExec := range execRun.Tasks {
			if taskExec.State == execution.TaskNeedsApproval {
				pendingAppID = taskExec.ApprovalID
				break
			}
		}
		if pendingAppID == "" {
			t.Fatalf("run needs approval but no task has ApprovalID")
		}

		// PROVE: Runtime approval enforced
		appRec, err := runtime.Execution().Engine().ApprovalManager().GetApproval(pendingAppID)
		if err != nil {
			t.Fatalf("GetApproval: %v", err)
		}
		if appRec.Status != execution.ApprovalRequested {
			t.Fatalf("expected approval status REQUESTED, got %s", appRec.Status)
		}

		// Operator approves
		if err := runtime.Execution().Approve(ctx, pendingAppID, "test-operator", "Approved after architectural review"); err != nil {
			t.Fatalf("Approve: %v", err)
		}

		// Resume execution
		execRun, err = runtime.Execution().ExecuteRun(ctx, run.RunID)
		if err != nil && execRun == nil {
			t.Fatalf("ExecuteRun after approve: %v", err)
		}
	}

	// PROVE: Final execution state
	if execRun.State != execution.RunDonePendingVerification {
		t.Fatalf("expected RunDonePendingVerification, got %s", execRun.State)
	}

	// PROVE: Worker assignment follows plan
	if len(executedTasks) != 2 {
		t.Fatalf("expected 2 tasks executed, got %d: %v", len(executedTasks), executedTasks)
	}
	if executedTasks[0] != "task-1" || executedTasks[1] != "task-2" {
		t.Fatalf("DAG order not preserved: %v", executedTasks)
	}

	// PROVE: Fallback / Constraints preserved in received packages
	for _, pkg := range receivedPackages {
		if pkg.GoalOutcome != goal.DesiredOutcome {
			t.Fatalf("constraint package lost goal outcome: got %q, want %q", pkg.GoalOutcome, goal.DesiredOutcome)
		}
		foundHard := false
		for _, hc := range pkg.HardConstraints {
			if strings.Contains(hc, "Do not touch security package") {
				foundHard = true
				break
			}
		}
		if !foundHard {
			t.Fatalf("task %s constraint package missing hard constraint", pkg.TaskID)
		}
	}

	// Phase 4: Process 06 Verification Handoff Bundle
	bundle, err := runtime.Execution().AssembleHandoffBundle(ctx, run.RunID)
	if err != nil {
		t.Fatalf("AssembleHandoffBundle: %v", err)
	}

	// PROVE: Process 06 Bundle Integrity
	if bundle.RunID != run.RunID {
		t.Fatalf("bundle runID mismatch: got %s, want %s", bundle.RunID, run.RunID)
	}
	if bundle.PlanID != approvedPlan.ID {
		t.Fatalf("bundle planID mismatch: got %s, want %s", bundle.PlanID, approvedPlan.ID)
	}
	if bundle.FinalState != execution.RunDonePendingVerification {
		t.Fatalf("bundle final state mismatch: got %s, want %s", bundle.FinalState, execution.RunDonePendingVerification)
	}
	if len(bundle.Tasks) != 2 {
		t.Fatalf("bundle expected 2 tasks, got %d", len(bundle.Tasks))
	}
	for _, taskExec := range bundle.Tasks {
		if taskExec.State != execution.TaskCompletedPendingVerify {
			t.Fatalf("task %s in bundle has state %s, expected TaskCompletedPendingVerify", taskExec.TaskID, taskExec.State)
		}
	}
	if len(bundle.EvidenceBundle) == 0 {
		t.Fatalf("bundle has no evidence references")
	}

	// PROVE: Process 06 receives all verification obligations
	if len(bundle.VerificationObligations.Obligations) == 0 {
		t.Fatalf("bundle expected verification obligations from plan, got 0")
	}
}

func TestProcess05_FailureChain_SafeReturn(t *testing.T) {
	ctx := context.Background()
	repo := runtimeIntegrationRepo(t)
	projectDir := repo.Path()

	if _, err := app.Bootstrap(ctx, projectDir); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	runtime, err := app.Open(ctx, projectDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer runtime.Close()

	sessionID := "SESSION-P05-FAIL"
	projectID := projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")

	goal := model.GoalContract{
		ID:                  "GOAL-P05-FAIL",
		SessionID:           sessionID,
		Revision:            1,
		ProjectID:           string(projectID),
		OriginalRequest:     "Failing task execution test",
		ConstitutionVersion: constitution.Current.String(),
		Confirmation:        model.ConfirmationApproved,
		DesiredOutcome:      "Test failure handling",
		ExpectedArtifact:    "fail.txt",
		SuccessCriteria:     []string{"fail.txt exists"},
		Risk:                model.R1,
		AuthoritySource:     "operator",
	}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("SaveGoalContract: %v", err)
	}

	now := time.Now().UTC()
	cap := goalintake.UnknownCapacity("openai", true)
	createReq := app.CreatePlanRequest{
		SessionID: sessionID,
		ProjectID: projectID,
		Tasks: []plan.Task{
			{
				ID:       "task-fail",
				Title:    "task that fails",
				Mutating: true,
				Paths:    []string{"fail.txt"},
				Criteria: []string{"fail.txt exists"},
			},
		},
		Candidates: []goalintake.Candidate{
			{Provider: "openai", Model: "gpt-4o", Capacity: cap, Governance: constitution.GovernanceVerified},
		},
		HarnessCandidates: []plan.HarnessCandidate{
			{
				Profile: model.HarnessProfile{
					Harness:         "mock-failing-harness",
					InstalledVersion: "1.0.0",
					SupportedModels: []string{"gpt-4o"},
					DefaultModel:    "gpt-4o",
					ProbeEvidenceID: "EV-mock-fail",
					ProbedAt:        now,
				},
				InstalledVersion: "1.0.0",
				Provider:         "openai",
				Capacity:         cap,
			},
		},
	}

	if _, err := runtime.Plans().Create(ctx, createReq); err != nil {
		t.Fatalf("Plans.Create: %v", err)
	}
	if _, err := runtime.Plans().Approve(ctx, projectID); err != nil {
		t.Fatalf("Plans.Approve: %v", err)
	}

	// Register a harness that returns a task failure under the routed provider name "openai"
	runtime.Execution().RegisterHarness(execution.NewMockHarness("openai", func(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		return execution.TaskResult{
			TaskID:       task.TaskID,
			Success:      false,
			ErrorMessage: "Simulated worker compilation failure",
		}, nil
	}))

	run, err := runtime.Execution().StartRun(ctx, sessionID, projectID)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Execute run - if it pauses for approval, approve and continue
	execRun, err := runtime.Execution().ExecuteRun(ctx, run.RunID)
	for execRun != nil && execRun.State == execution.RunNeedsApproval {
		var appID string
		for _, taskExec := range execRun.Tasks {
			if taskExec.State == execution.TaskNeedsApproval {
				appID = taskExec.ApprovalID
				break
			}
		}
		if appID != "" {
			if err := runtime.Execution().Approve(ctx, appID, "operator", "proceed to test failure"); err != nil {
				t.Fatalf("Approve: %v", err)
			}
			execRun, err = runtime.Execution().ExecuteRun(ctx, run.RunID)
		}
	}

	if err == nil {
		t.Fatalf("expected ExecuteRun to fail, got success with state %s", execRun.State)
	}

	// PROVE: Run transitions to RunFailed
	latestRun, err := runtime.Execution().GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if latestRun.State != execution.RunFailed {
		t.Fatalf("expected run state RunFailed, got %s", latestRun.State)
	}

	// PROVE: Task is marked TaskFailed with reason preserved
	failedTask, ok := latestRun.Tasks["task-fail"]
	if !ok || failedTask.State != execution.TaskFailed {
		t.Fatalf("expected task-fail to be in TaskFailed state, got %v", failedTask.State)
	}
	if !strings.Contains(failedTask.LastFailureReason, "Simulated worker compilation failure") {
		t.Fatalf("expected failure reason preserved, got %q", failedTask.LastFailureReason)
	}

	// PROVE: Fail-closed Process 06 gate rejects handoff bundle on failed run
	_, err = runtime.Execution().AssembleHandoffBundle(ctx, run.RunID)
	if err == nil {
		t.Fatalf("expected AssembleHandoffBundle to reject failed run, got nil error")
	}
	if !errors.Is(err, execution.ErrRunBlocked) && !errors.Is(err, execution.ErrRunInvalid) {
		t.Fatalf("expected ErrRunBlocked or ErrRunInvalid, got: %v", err)
	}

	// PROVE: Rollback to baseline checkpoint succeeds
	if len(latestRun.Checkpoints) > 0 {
		cpID := latestRun.Checkpoints[0].CheckpointID
		restoredCP, err := runtime.Execution().Rollback(ctx, cpID)
		if err != nil {
			t.Fatalf("Rollback failed: %v", err)
		}
		if restoredCP.CheckpointID != cpID {
			t.Fatalf("restored checkpoint mismatch: got %s, want %s", restoredCP.CheckpointID, cpID)
		}
	}
}
