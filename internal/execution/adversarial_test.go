package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestAdversarial_FakeApprovalInjectionAndReplay(t *testing.T) {
	mgr := NewApprovalManager()
	now := time.Now().UTC()

	req := ApprovalRequest{
		RunID:          "run-adv-01",
		TaskID:         "task-1",
		PlanID:         "plan-adv",
		PlanVersion:    1,
		OperationType:  "DATABASE_MIGRATION",
		TargetResource: "schema.sql",
		RiskLevel:      model.R3,
		Scope:          "drop table",
		Parameters:     "DROP TABLE users;",
	}

	app, err := mgr.RequestApproval(req)
	if err != nil {
		t.Fatalf("RequestApproval failed: %v", err)
	}

	// 1. Operator approves the requested action
	decidedApp, err := mgr.Decide(app.ApprovalID, true, "alice", "approved db migration", now)
	if err != nil {
		t.Fatalf("Decide failed: %v", err)
	}

	// Verify and consume valid decision (one-shot)
	actionDigest := ComputeActionDigest(req.OperationType, req.TargetResource, req.DiffPreview, req.Parameters)
	if err := mgr.ValidateAndConsume(decidedApp.ApprovalID, actionDigest, "", now); err != nil {
		t.Fatalf("legitimate approval should verify and consume: %v", err)
	}

	// Replay attack: Adversary tries to reuse the consumed approval
	err = mgr.ValidateAndConsume(decidedApp.ApprovalID, actionDigest, "", now)
	if err == nil {
		t.Fatalf("expected replay attack of consumed approval to fail, got nil")
	}

	// 2. Attack: Adversary modifies the action parameters (TOCTOU action tampering)
	app2, err := mgr.RequestApproval(req)
	if err != nil {
		t.Fatalf("RequestApproval failed: %v", err)
	}
	decidedApp2, err := mgr.Decide(app2.ApprovalID, true, "alice", "approved db migration", now)
	if err != nil {
		t.Fatalf("Decide failed: %v", err)
	}
	tamperedActionDigest := ComputeActionDigest(req.OperationType, req.TargetResource, req.DiffPreview, "DROP DATABASE production; -- tampered")
	err = mgr.ValidateAndConsume(decidedApp2.ApprovalID, tamperedActionDigest, "", now)
	if err == nil {
		t.Fatalf("expected action digest mismatch for tampered action, got nil")
	}
	if !errors.Is(err, ErrApprovalTOCTOUViolation) {
		t.Fatalf("expected ErrApprovalTOCTOUViolation, got: %v", err)
	}

	// 3. Attack: Adversary attempts to use a forged or non-existent approval ID
	err = mgr.ValidateAndConsume("forged-approval-uuid", actionDigest, "", now)
	if err == nil {
		t.Fatalf("expected error for non-existent approval ID, got nil")
	}

	// 4. Attack: Adversary attempts to use expired approval
	expiredApp, _ := mgr.RequestApproval(ApprovalRequest{
		RunID:          "run-adv-01",
		TaskID:         "task-2",
		PlanID:         "plan-adv",
		PlanVersion:    1,
		OperationType:  "FILE_DELETE",
		TargetResource: "old.log",
	})
	pastTime := now.Add(-10 * time.Minute)
	expiredApp.ExpiresAt = &pastTime
	mgr.approvals[expiredApp.ApprovalID] = expiredApp

	expiredActionDigest := ComputeActionDigest("FILE_DELETE", "old.log", "", "")
	err = mgr.ValidateAndConsume(expiredApp.ApprovalID, expiredActionDigest, "", now)
	if err == nil {
		t.Fatalf("expected error for expired approval, got nil")
	}
	if !errors.Is(err, ErrApprovalTOCTOUViolation) {
		t.Fatalf("expected ErrApprovalTOCTOUViolation for expired approval, got: %v", err)
	}
}

func TestAdversarial_DroppedHardConstraint(t *testing.T) {
	now := time.Now().UTC()
	goal, p := createTestGoalAndPlan(now)
	task := TaskExecution{
		TaskID:      "task-adv-1",
		Description: "Perform mutation",
	}

	pkg, err := BuildConstraintPackage(goal, task, p)
	if err != nil {
		t.Fatalf("BuildConstraintPackage failed: %v", err)
	}

	// Valid package passes verification
	if err := VerifyConstraintPackage(pkg); err != nil {
		t.Fatalf("valid package failed verification: %v", err)
	}

	// 1. Attack: Worker drops hard constraints
	droppedPkg := pkg
	droppedPkg.HardConstraints = nil
	err = VerifyConstraintPackage(droppedPkg)
	if err == nil {
		t.Fatalf("expected verification failure when hard constraints dropped, got nil")
	}
	if !errors.Is(err, ErrConstraintViolation) {
		t.Fatalf("expected ErrConstraintViolation, got: %v", err)
	}

	// 2. Attack: Worker modifies hard constraint text
	alteredPkg := pkg
	alteredPkg.HardConstraints = []string{"Can touch production database"}
	err = VerifyConstraintPackage(alteredPkg)
	if err == nil {
		t.Fatalf("expected digest mismatch when constraint altered, got nil")
	}
	if !errors.Is(err, ErrConstraintViolation) {
		t.Fatalf("expected ErrConstraintViolation, got: %v", err)
	}
}

func TestAdversarial_TwoWorkersRaceSameResource(t *testing.T) {
	lm := NewLeaseManager()
	now := time.Now().UTC()

	// Worker 1 acquires lease on "internal/auth"
	lease1, err := lm.AcquireLease(AcquireLeaseRequest{
		RunID:           "run-race",
		TaskID:          "task-1",
		AgentID:         "agent-worker-1",
		Role:            "security-engineer",
		ScopedResources: []string{"internal/auth/login.go"},
		MutationScope:   "edit",
		TTL:             5 * time.Minute,
		Now:             now,
	})
	if err != nil {
		t.Fatalf("first lease acquisition failed: %v", err)
	}
	if lease1.Status != LeaseActive {
		t.Fatalf("expected lease1 active, got: %s", lease1.Status)
	}

	// Worker 2 attempts to acquire lease on overlapping directory "internal/auth"
	_, err = lm.AcquireLease(AcquireLeaseRequest{
		RunID:           "run-race",
		TaskID:          "task-2",
		AgentID:         "agent-worker-2",
		Role:            "backend-engineer",
		ScopedResources: []string{"internal/auth"},
		MutationScope:   "edit",
		TTL:             5 * time.Minute,
		Now:             now,
	})
	if err == nil {
		t.Fatalf("expected lease conflict for overlapping resource, got nil")
	}
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("expected ErrLeaseConflict, got: %v", err)
	}

	// Worker 2 attempts to acquire lease on exact same file "internal/auth/login.go"
	_, err = lm.AcquireLease(AcquireLeaseRequest{
		RunID:           "run-race",
		TaskID:          "task-3",
		AgentID:         "agent-worker-3",
		Role:            "backend-engineer",
		ScopedResources: []string{"internal/auth/login.go"},
		MutationScope:   "edit",
		TTL:             5 * time.Minute,
		Now:             now,
	})
	if err == nil {
		t.Fatalf("expected lease conflict for exact file overlap, got nil")
	}
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("expected ErrLeaseConflict, got: %v", err)
	}

	// Worker 1 releases lease
	err = lm.ReleaseLease(lease1.LeaseID, "agent-worker-1", now)
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// Now worker 2 can acquire lease successfully
	lease2, err := lm.AcquireLease(AcquireLeaseRequest{
		RunID:           "run-race",
		TaskID:          "task-2",
		AgentID:         "agent-worker-2",
		Role:            "backend-engineer",
		ScopedResources: []string{"internal/auth"},
		MutationScope:   "edit",
		TTL:             5 * time.Minute,
		Now:             now,
	})
	if err != nil {
		t.Fatalf("lease acquisition after release should succeed: %v", err)
	}
	if lease2.Status != LeaseActive {
		t.Fatalf("expected lease2 active, got: %s", lease2.Status)
	}
}

func TestAdversarial_StaleEvidenceAfterFileMutation(t *testing.T) {
	oracle := NewEvidenceOracle()
	now := time.Now().UTC()
	runID := "run-ev-01"

	// 1. Record evidence that verified auth.go
	ev, err := oracle.RecordEvidence(ExecutionEvidence{
		EvidenceID:    "ev-auth-01",
		RunID:         runID,
		TaskID:        "task-test-auth",
		ToolName:      "go test",
		RelevantFiles: []string{"internal/auth/auth.go"},
		OutputDigest:  "sha256:111111",
		CreatedAt:     now,
	})
	if err != nil {
		t.Fatalf("RecordEvidence failed: %v", err)
	}
	if ev.Status != EvidenceValid {
		t.Fatalf("expected valid initial status, got: %s", ev.Status)
	}

	// 2. An adversary or subsequent task mutates "internal/auth/auth.go"
	later := now.Add(2 * time.Minute)
	invalidated := oracle.InvalidateByFiles(runID, []string{"internal/auth/auth.go"}, "File mutated by task-mut-02", later)
	if len(invalidated) != 1 || invalidated[0] != "ev-auth-01" {
		t.Fatalf("expected ev-auth-01 invalidated, got: %v", invalidated)
	}

	// 3. Check evidence status in oracle
	fetched, err := oracle.GetEvidence("ev-auth-01")
	if err != nil {
		t.Fatalf("GetEvidence failed: %v", err)
	}
	if fetched.Status != EvidenceStale {
		t.Fatalf("expected EvidenceStale status after file mutation, got: %s", fetched.Status)
	}
}

func TestAdversarial_WorkerChangesScopeOutsideTargetFiles(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "scoped.txt"), []byte("scoped data"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "critical_system.go"), []byte("security core"), 0644)

	wm, err := NewWorktreeManager(tmpDir)
	if err != nil {
		t.Fatalf("NewWorktreeManager failed: %v", err)
	}

	wtPath, err := wm.PrepareWorktree(ctx, "task-scope-adv", "run-adv")
	if err != nil {
		t.Fatalf("PrepareWorktree failed: %v", err)
	}
	defer wm.CleanWorktree(ctx, wtPath)

	// Worker illicitly mutates critical_system.go
	_ = os.WriteFile(filepath.Join(wtPath, "critical_system.go"), []byte("backdoor injected!"), 0644)

	// Reconcile scoped to ONLY scoped.txt -> must reject mutation!
	_, err = wm.ReconcileChanges(wtPath, []string{"scoped.txt"})
	if err == nil {
		t.Fatalf("expected scope violation error when un-scoped file modified, got nil")
	}
	if !errors.Is(err, ErrIsolationCompromised) {
		t.Fatalf("expected ErrIsolationCompromised, got: %v", err)
	}

	// Confirm project root was NOT compromised
	rootContent, _ := os.ReadFile(filepath.Join(tmpDir, "critical_system.go"))
	if string(rootContent) != "security core" {
		t.Fatalf("CRITICAL SECURITY FAILURE: project root was modified by rogue worker!")
	}
}
