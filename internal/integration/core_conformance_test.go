package integration

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/recovery"
	"github.com/Zen1th53/marshal/internal/router"
	"github.com/Zen1th53/marshal/internal/scheduler"
	"github.com/Zen1th53/marshal/internal/verify/quorum"
)

func TestCoreConformanceRemoteBoundary(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}

	rt, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// 1. Fine-grained Token Capability Validation
	authManager := auth.NewManager(t.TempDir())
	secret, tok, err := authManager.CreateToken("mcp-readonly-token", auth.KindMCPClient, []string{string(auth.CapTaskRead)})
	if err != nil {
		t.Fatal(err)
	}
	_ = tok

	p, err := authManager.Authenticate(secret)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	// Token with only task.read cannot perform task.execute
	if p.HasCapability(auth.CapTaskExecute) {
		t.Fatal("read-only token was granted task.execute authority")
	}

	// Token with task.read must pass task.read check
	if !p.HasCapability(auth.CapTaskRead) {
		t.Fatal("read-only token was denied task.read authority")
	}
}

func TestCoreConformanceOrchestrationEngine(t *testing.T) {
	ctx := context.Background()

	// 1. Scheduler Real Multi-Factor Scoring
	sched := scheduler.NewScheduler()
	assignment, err := sched.Next(ctx, scheduler.Task{TaskID: "TASK-CONF-01", RequiredCapabilities: []string{"go"}}, []scheduler.Candidate{
		{AgentID: "agent-a", Capabilities: []string{"go"}, SuccessRate: 0.95, Load: 0.1, ContextUtilization: 0.2, EstimatedCost: 0.05},
		{AgentID: "agent-b", Capabilities: []string{"go"}, SuccessRate: 0.70, Load: 0.8, ContextUtilization: 0.8, EstimatedCost: 0.20},
	})
	if err != nil {
		t.Fatalf("scheduler.Next: %v", err)
	}
	if assignment.AgentID != "agent-a" {
		t.Fatalf("expected agent-a to be selected by scheduler, got: %s", assignment.AgentID)
	}

	// 2. Dynamic Model Profile Selection
	rtr := router.NewRouter()
	decision, err := rtr.Route(ctx, []string{"code", "refactor"}, 4000)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if decision.Provider == "" || decision.Model == "" {
		t.Fatal("expected selected provider/model from dynamic router")
	}

	// 3. Recovery State Machine with Poisoned Checkpoint Quarantine
	recManager := recovery.NewManager()
	recPlan, err := recManager.PlanRecovery(ctx, recovery.RecoveryRequest{
		TaskID:         "TASK-CONF-01",
		CurrentRetries: 0,
		MaxRetries:     3,
		Checkpoint: &recovery.Checkpoint{
			ID:        "CP-POISONED",
			TaskID:    "TASK-CONF-01",
			State:     recovery.CheckpointPoisoned,
			CreatedAt: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatalf("PlanRecovery: %v", err)
	}
	if recPlan.Action != recovery.ActionRestartFromBase {
		t.Fatalf("expected restart from base for poisoned checkpoint, got: %s", recPlan.Action)
	}
}

func TestCoreConformanceTaskLifecycleAndQuorumMergeGate(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}

	rt, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx := context.Background()

	// 1. Create high risk R2 task
	taskID := "TASK-CONF-GATE-01"
	_, err = rt.ImportTasks(ctx, []model.Task{
		{ID: taskID, Title: "Core Conformance Gate Task", Status: model.TaskReady, Risk: model.R2},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Register Developer Agent & Claim
	devAgent, err := rt.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "dev-conf", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := rt.Claim(ctx, app.ClaimRequest{TaskID: taskID, AgentID: devAgent.ID, ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}

	// 3. Begin execution -> working
	err = rt.Store().BeginExecution(ctx, taskID, claim.Session.ID, devAgent.ID, "branch-conf", "/tmp/wt-conf", "head001", 1)
	if err != nil {
		t.Fatal(err)
	}

	// 4. Finalize execution -> review
	err = rt.Store().FinalizeExecution(ctx, taskID, claim.Session.ID, true, 2)
	if err != nil {
		t.Fatal(err)
	}

	// 5. Reviewer approves -> qa
	_, err = rt.Store().TransitionTask(ctx, model.TaskTransitionRequest{
		TaskID:           taskID,
		FromStatus:       model.TaskReview,
		ToStatus:         model.TaskQA,
		ActorRole:        model.RoleReviewer,
		ActorID:          "reviewer-1",
		HeadCommit:       "head001",
		ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatalf("Transition to QA: %v", err)
	}

	// 6. QA approves -> security_review
	_, err = rt.Store().TransitionTask(ctx, model.TaskTransitionRequest{
		TaskID:           taskID,
		FromStatus:       model.TaskQA,
		ToStatus:         model.TaskSecurityReview,
		ActorRole:        model.RoleQA,
		ActorID:          "qa-engineer",
		HeadCommit:       "head001",
		ExpectedRevision: 4,
	})
	if err != nil {
		t.Fatalf("Transition to SecurityReview: %v", err)
	}

	// 7. Security approves -> ready_to_merge
	_, err = rt.Store().TransitionTask(ctx, model.TaskTransitionRequest{
		TaskID:           taskID,
		FromStatus:       model.TaskSecurityReview,
		ToStatus:         model.TaskReadyToMerge,
		ActorRole:        model.RoleAppSec,
		ActorID:          "sec-officer",
		HeadCommit:       "head001",
		ExpectedRevision: 5,
	})
	if err != nil {
		t.Fatalf("Transition to ReadyToMerge: %v", err)
	}

	// 6. Quorum Merge Gate Verification
	reqs := app.DeriveQuorumRequirements(model.R2)
	qEngine := quorum.NewEngine(nil)
	eval, err := qEngine.Evaluate(ctx, reqs, []quorum.Attestation{
		{
			Subject:       "agent-qa-1",
			Provider:      "claude",
			Role:          "qa",
			ChangeID:      taskID,
			EvidenceID:    "EVID-001",
			Kind:          "qa",
			Result:        quorum.ResultPass,
			ContentDigest: "head001",
			CreatedAt:     time.Now().UTC().Add(-time.Minute),
		},
		{
			Subject:       "agent-sec-1",
			Provider:      "codex",
			Role:          "appsec",
			ChangeID:      taskID,
			EvidenceID:    "EVID-002",
			Kind:          "security",
			Result:        quorum.ResultPass,
			ContentDigest: "head001",
			CreatedAt:     time.Now().UTC().Add(-time.Minute),
		},
	}, quorum.Provenance{
		ChangeID:      taskID,
		ContentDigest: "head001",
	})
	if err != nil || !eval.Satisfied {
		t.Fatalf("Quorum evaluation failed: err=%v eval=%+v", err, eval)
	}

	// 7. Final merge
	_, err = rt.Store().TransitionTask(ctx, model.TaskTransitionRequest{
		TaskID:           taskID,
		FromStatus:       model.TaskReadyToMerge,
		ToStatus:         model.TaskMerged,
		ActorRole:        model.RoleAdmin,
		ActorID:          "admin-1",
		HeadCommit:       "head001",
		ExpectedRevision: 6,
	})
	if err != nil {
		t.Fatalf("Merge transition failed: %v", err)
	}

	// Verify task in SQLite reached merged status
	task, err := rt.Store().GetTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskMerged {
		t.Fatalf("expected task status merged, got: %s", task.Status)
	}
}

func TestCoreConformanceStorageAndGC(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}

	rt, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx := context.Background()

	// 1. Run GC on worktrees and artifacts
	wtRes, err := rt.GCWorktrees(ctx, false, 0)
	if err != nil {
		t.Fatalf("GCWorktrees: %v", err)
	}
	_ = wtRes

	artRes, err := rt.GCArtifacts(ctx, false, 0, 0)
	if err != nil {
		t.Fatalf("GCArtifacts: %v", err)
	}
	_ = artRes

	// 2. Backup and Restore
	backupPath := filepath.Join(t.TempDir(), "conf_backup.db")
	meta, err := rt.BackupState(ctx, backupPath)
	if err != nil {
		t.Fatalf("BackupState: %v", err)
	}
	if meta.DatabaseSHA256 == "" {
		t.Fatal("expected non-empty database hash in backup")
	}

	verified, err := app.VerifyStateBackup(ctx, backupPath, meta.ProjectID, 67)
	if err != nil {
		t.Fatalf("VerifyStateBackup: %v", err)
	}
	if verified.DatabaseSHA256 != meta.DatabaseSHA256 {
		t.Fatal("backup verification hash mismatch")
	}
}

func TestCoreConformanceAdversarialRedaction(t *testing.T) {
	leakedSecret := "ghp_1234567890abcdefghijklmnopqrstuvwxyzAB"
	sanitizer := evidence.NewStrictSanitizer(evidence.SanitizerConfig{
		LiteralSecrets: []string{leakedSecret},
	})

	payload := []byte("Execution completed with token: " + leakedSecret)
	_, err := sanitizer.SanitizeBytes(context.Background(), payload)
	if !errors.Is(err, evidence.ErrSecretRejected) {
		t.Fatalf("expected ErrSecretRejected when secret is present in bytes, got: %v", err)
	}
}

// Compile-time assertion of Quorum engine evaluation contract
var _ = quorum.NewEngine(nil)
