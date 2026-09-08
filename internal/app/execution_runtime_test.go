package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestExecutionService_GateRefusalIfPlanUnapproved(t *testing.T) {
	runtime := runtimeForPlan(t)
	pService := runtime.Plans()

	// Create plan in DRAFT state
	_, err := pService.Create(context.Background(), planCreateRequest())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	execService := runtime.Execution()

	// Attempting to start run without approval must be refused by the gate
	_, err = execService.StartRun(context.Background(), "SESSION-plan", runtimePlanProject)
	if err == nil {
		t.Fatalf("expected StartRun to fail for unapproved plan, got nil")
	}
}

func TestExecutionService_StartRunAndExecute_Success(t *testing.T) {
	runtime := runtimeForPlan(t)
	pService := runtime.Plans()

	// 1. Create plan
	_, err := pService.Create(context.Background(), planCreateRequest())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	// 2. Approve plan
	_, err = pService.Approve(context.Background(), runtimePlanProject)
	if err != nil {
		t.Fatalf("approve plan: %v", err)
	}

	execService := runtime.Execution()

	// 3. Register mock worker harness that reports valid completion and evidence
	execService.RegisterHarness(execution.NewMockHarness("test-harness", func(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		return execution.TaskResult{
			TaskID:  task.TaskID,
			Success: true,
			Claims: []execution.ExecutionClaim{
				{
					ClaimID:      "claim-" + task.TaskID,
					TaskID:       task.TaskID,
					ClaimText:    "fixed typo in README.md",
					Status:       execution.ClaimSupported,
					EvidenceRefs: []string{"ev-readme"},
				},
			},
		}, nil
	}))

	// Register evidence in oracle so claim is supported
	execService.Engine().EvidenceOracle().RecordEvidence(execution.ExecutionEvidence{
		EvidenceID:    "ev-readme",
		RunID:         "RUN-ANY",
		RelevantFiles: []string{"README.md"},
		Status:        execution.EvidenceValid,
	})

	// 4. Start execution run
	run, err := execService.StartRun(context.Background(), "SESSION-plan", runtimePlanProject)
	if err != nil {
		t.Fatalf("StartRun failed: %v", err)
	}
	if run.State != execution.RunReady {
		t.Fatalf("expected run state RunReady, got %s", run.State)
	}
	execService.Engine().EvidenceOracle().RecordEvidence(execution.ExecutionEvidence{
		EvidenceID: "ev-readme", RunID: run.RunID, RelevantFiles: []string{"README.md"}, Status: execution.EvidenceValid,
	})

	// 5. Execute run
	completedRun, err := execService.ExecuteRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("ExecuteRun failed: %v", err)
	}
	if completedRun.State == execution.RunNeedsApproval {
		var appID string
		for _, tsk := range completedRun.Tasks {
			if tsk.ApprovalID != "" {
				appID = tsk.ApprovalID
				break
			}
		}
		if appID == "" {
			t.Fatalf("run in NEEDS_APPROVAL state but no task has an approval ID")
		}
		if err := execService.Approve(context.Background(), appID, "test-operator", "Approve task execution"); err != nil {
			t.Fatalf("approve failed: %v", err)
		}
		completedRun, err = execService.ExecuteRun(context.Background(), run.RunID)
		if err != nil {
			t.Fatalf("ExecuteRun after approval failed: %v", err)
		}
	}
	if completedRun.State != execution.RunDonePendingVerification {
		t.Fatalf("expected run state RunDonePendingVerification, got %s", completedRun.State)
	}

	// 6. Assemble Process 06 handoff bundle
	bundle, err := execService.AssembleHandoffBundle(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("AssembleHandoffBundle failed: %v", err)
	}
	if bundle.FinalState != execution.RunDonePendingVerification {
		t.Fatalf("expected bundle final state RunDonePendingVerification, got %s", bundle.FinalState)
	}
	if len(bundle.Tasks) == 0 {
		t.Fatalf("expected bundle to have tasks, got 0")
	}
	if bundle.FinalGitTree == "" {
		t.Fatal("Process 06 handoff was not bound to an exact git tree")
	}
	// 7. Process 06 independently evaluates the handoff and persists an exact
	// completion attestation. The execution claim alone is not used as proof.
	binding, err := runtime.Verification().authoritativeBinding(context.Background(), bundle.RunID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := verification.Session{ID: "verify-" + run.RunID, Version: 1, Binding: binding,
		Criteria:       []verification.Criterion{{ID: "typo-fixed", Mandatory: true, ClaimIDs: []string{"claim-fix"}}},
		Claims:         []verification.Claim{{ID: "claim-fix", CriterionID: "typo-fixed", SemanticScope: []string{"README.md"}, EvidenceIDs: []string{"verify-readback"}}},
		Evidence:       []verification.Evidence{{ID: "verify-readback", ClaimID: "claim-fix", Status: verification.StatusPass, ContentDigest: "readback", TreeDigest: binding.TreeDigest, EnvironmentDigest: binding.EnvironmentDigest, ClusterID: "independent-readback", Attempts: 1, Passes: 1}},
		RequiredChecks: map[string]verification.Status{"security": verification.StatusPass, "runtime_negative": verification.StatusPass}, CreatedAt: now, UpdatedAt: now}
	if _, err := runtime.Verification().Start(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	verified, err := runtime.Verification().Evaluate(context.Background(), session.ID)
	if err != nil || verified.State != verification.VerifiedComplete {
		current, currentErr := runtime.Verification().authoritativeBinding(context.Background(), bundle.RunID)
		t.Fatalf("verification = %+v, %v; session binding=%+v current=%+v current_err=%v", verified, err, session.Binding, current, currentErr)
	}
	attestation, err := runtime.Verification().Attest(context.Background(), session.ID, "bundle-digest", "full-chain-e2e")
	if err != nil || attestation.Decision != verification.VerifiedComplete {
		t.Fatalf("attestation = %s, %v", attestation.Decision, err)
	}
}

func TestExecutionService_Rollback(t *testing.T) {
	runtime := runtimeForPlan(t)
	pService := runtime.Plans()

	_, err := pService.Create(context.Background(), planCreateRequest())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	_, err = pService.Approve(context.Background(), runtimePlanProject)
	if err != nil {
		t.Fatalf("approve plan: %v", err)
	}

	execService := runtime.Execution()
	run, err := execService.StartRun(context.Background(), "SESSION-plan", runtimePlanProject)
	if err != nil {
		t.Fatalf("StartRun failed: %v", err)
	}

	if len(run.Checkpoints) == 0 {
		t.Fatalf("expected initial checkpoint on run start")
	}

	// Restore checkpoint
	initialCP := run.Checkpoints[0].CheckpointID
	restored, err := execService.Rollback(context.Background(), initialCP)
	if err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}
	if restored.RestoredAt == nil {
		t.Fatalf("expected RestoredAt to be set after rollback")
	}
}
