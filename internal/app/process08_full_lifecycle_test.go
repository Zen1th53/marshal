package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/optimization"
	"github.com/Zen1th53/marshal/internal/verification"
)

// TestProcess03Through08Lifecycle uses the actual runtime boundaries from a
// Process 03 formed and approved goal through Process 08 replay and rollback.
// The mock worker only stands in for the isolated task harness; planning,
// execution, verification, attestation, learning and optimization are each
// their real durable services.
func TestProcess03Through08Lifecycle(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	// Process 03: form the user request before any plan exists, preserve the
	// raw request/constraints, and persist only an approved canonical contract.
	intake, err := goalintake.Form(goalintake.FormationRequest{
		Request:   "Fix the documented typo. Do not change the public API.",
		ProjectID: runtimePlanProject, SessionID: "SESSION-p03-p08",
		Version: constitution.Current,
		Context: goalintake.RequestContext{Recoverable: true, ScopeKnown: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	intake = goalintake.Approve(intake)
	if !intake.Confirmation.Settled() || len(intake.Constraints) == 0 {
		t.Fatalf("Process 03 did not retain an approved constrained goal: %+v", intake)
	}
	goal := model.GoalContract{
		ID: "GOAL-p03-p08", SessionID: intake.SessionID, ProjectID: string(intake.ProjectID),
		OriginalRequest: intake.OriginalRequest, RequestDigest: intake.RequestDigest,
		ConstitutionVersion: intake.Version.String(), Confirmation: model.ConfirmationApproved,
		Assessment: map[string]string{"source": "goalintake"}, DesiredOutcome: "The documented typo is fixed",
		ExpectedArtifact: "README.md", Scope: []string{"README.md"}, Constraints: intake.Constraints,
		SuccessCriteria: []string{"the typo is fixed"}, Risk: model.R1, AuthoritySource: "operator",
		UnderstandingState: model.GoalReady, Revision: 1,
	}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}

	// Process 04: the actual planning service reads the active canonical goal.
	request := planCreateRequest()
	request.SessionID = goal.SessionID
	created, err := runtime.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Goal.GoalID != goal.ID {
		t.Fatalf("plan lost Process 03 goal binding: %+v", created.Goal)
	}
	if _, err := runtime.Plans().Approve(ctx, runtimePlanProject); err != nil {
		t.Fatal(err)
	}

	// Process 05: run the approved plan through the real execution service.
	execService := runtime.Execution()
	execService.RegisterHarness(execution.NewMockHarness("test-harness", func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error) {
		return execution.TaskResult{TaskID: "fix", Success: true, Claims: []execution.ExecutionClaim{{
			ClaimID: "claim-fix", TaskID: "fix", ClaimText: "fixed typo in README.md",
			Status: execution.ClaimSupported, EvidenceRefs: []string{"ev-readme"},
		}}}, nil
	}))
	run, err := execService.StartRun(ctx, goal.SessionID, runtimePlanProject)
	if err != nil {
		t.Fatal(err)
	}
	execService.Engine().EvidenceOracle().RecordEvidence(execution.ExecutionEvidence{EvidenceID: "ev-readme", RunID: run.RunID, RelevantFiles: []string{"README.md"}, Status: execution.EvidenceValid})
	completed, err := execService.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State == execution.RunNeedsApproval {
		var approvalID string
		for _, task := range completed.Tasks {
			if task.ApprovalID != "" {
				approvalID = task.ApprovalID
				break
			}
		}
		if approvalID == "" {
			t.Fatal("execution requested approval without an approval id")
		}
		if err := execService.Approve(ctx, approvalID, "test-operator", "approve bounded fixture task"); err != nil {
			t.Fatal(err)
		}
		completed, err = execService.ExecuteRun(ctx, run.RunID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if completed.State != execution.RunDonePendingVerification {
		t.Fatalf("execution state=%s", completed.State)
	}
	bundle, err := execService.AssembleHandoffBundle(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}

	// Process 06: independently verify the run and append a digest-bound
	// completion attestation. Process 07 derives its binding from this record.
	binding, err := runtime.Verification().authoritativeBinding(ctx, bundle.RunID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := verification.Session{ID: "verify-p03-p08", Version: 1, Binding: binding,
		Criteria:       []verification.Criterion{{ID: "typo-fixed", Mandatory: true, ClaimIDs: []string{"claim-fix"}}},
		Claims:         []verification.Claim{{ID: "claim-fix", CriterionID: "typo-fixed", SemanticScope: []string{"README.md"}, EvidenceIDs: []string{"verify-readback"}}},
		Evidence:       []verification.Evidence{{ID: "verify-readback", ClaimID: "claim-fix", Status: verification.StatusPass, ContentDigest: "readback", TreeDigest: binding.TreeDigest, EnvironmentDigest: binding.EnvironmentDigest, ClusterID: "independent-readback", Attempts: 1, Passes: 1}},
		RequiredChecks: map[string]verification.Status{"security": verification.StatusPass}, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := runtime.Verification().Start(ctx, session); err != nil {
		t.Fatal(err)
	}
	verified, err := runtime.Verification().Evaluate(ctx, session.ID)
	if err != nil || verified.State != verification.VerifiedComplete {
		t.Fatalf("verification=%+v err=%v", verified, err)
	}
	payload := []byte("p03-p08 independent evidence")
	sum := sha256.Sum256(payload)
	evidenceBundle, err := verification.BuildEvidenceBundle("bundle-p03-p08", session.ID, binding, []verification.BundleEntry{{Path: "evidence.txt", Digest: hex.EncodeToString(sum[:]), Size: int64(len(payload))}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Verification().Attest(ctx, session.ID, verification.BundleEnvelope{Bundle: evidenceBundle, Payloads: map[string][]byte{"evidence.txt": payload}}, "p03-p08-e2e"); err != nil {
		t.Fatal(err)
	}

	// Process 07: the learning service, not this test, derives entry bindings
	// from the completion attestation and seals the durable memory commit.
	commit, err := runtime.Learning().Commit(ctx, CommitInput{ID: "mc-p03-p08", Verification: session.ID, Provenance: "p03-p08-e2e", Candidates: []learning.PromotionInput{{
		Item:       learning.Item{ID: "memory-p03-p08", Claim: "bounded replay evidence is required before routing change", Scope: learning.ScopeProject, ProjectID: binding.ProjectID, State: model.ClaimStateVerified, Version: 1, Provenance: "p03-p08-e2e", Evidence: []learning.EvidenceRef{{ID: "learn-e1", ClusterID: "learn-c1", Digest: "d1", Kind: "verification"}, {ID: "learn-e2", ClusterID: "learn-c2", Digest: "d2", Kind: "verification"}}},
		Replayable: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}

	// Process 08: run an alternate route through the replay-only service path,
	// then prove an interrupted/missing-metric canary preserves rollback state.
	baseline := optimization.Baseline{ID: "baseline-p03-p08", MarshalSHA: binding.RunID, RoutingConfig: "baseline", VerifierPolicy: "independent", Toolchain: "test", EnvironmentHash: binding.EnvironmentDigest}
	candidate := optimization.Candidate{ID: "candidate-p03-p08", Dimension: optimization.DimRouting, Hypothesis: "evaluate governed alternate route", TaskScope: []string{"code"}, RollbackPlan: "restore baseline", VerificationPlan: "independent verifier", Provenance: "p03-p08-e2e"}
	gov := optimization.Governance{GovernableProviders: map[string]bool{"codex": true, "claude": true}, MaxCanaryExposure: .25, RequireRollback: true}
	cycle, err := runtime.Optimization().StartCycle(ctx, StartCycleInput{ID: "opt-p03-p08", MemoryCommitID: commit.ID, Objectives: []optimization.Objective{{Name: "verified_success", HigherIsBetter: true, Weight: 1}}, Candidates: []optimization.Candidate{candidate}, Baselines: []optimization.Baseline{baseline}, Provenance: "p03-p08-e2e", Governance: gov})
	if err != nil {
		t.Fatal(err)
	}
	factual := optimization.FactualRun{TaskID: "task-p03-p08", Route: optimization.Route{TaskClass: "code", Provider: "codex", ProviderVersion: "1", Model: "m", Harness: "test", HarnessVersion: "1", VerifierPolicy: "independent"}, Outcome: learning.OutcomeFailed, VerifierResult: optimization.StatusFail, ReplayClass: learning.ReplayExact, TreeDigest: binding.TreeDigest, EnvironmentDigest: binding.EnvironmentDigest}
	runner := &e2eReplayRunner{}
	if _, err := runtime.Optimization().ExecuteReplay(ctx, cycle.ID, runner, factual, optimization.Route{TaskClass: "code", Provider: "claude", ProviderVersion: "1", Model: "m", Harness: "test", HarnessVersion: "1", VerifierPolicy: "independent"}, optimization.SandboxPolicy{WritableRoot: t.TempDir(), MaxWallMillis: 1_000, MaxMemoryBytes: 1 << 20}, "p03-p08-e2e", "replay-cluster", gov); err != nil || !runner.called {
		t.Fatalf("real replay did not execute: called=%t err=%v", runner.called, err)
	}
	canary, err := runtime.Optimization().Canary(ctx, cycle.ID, "", optimization.Canary{ID: "canary-p03-p08", CandidateID: candidate.ID, BaselineID: baseline.ID, Exposure: .1, EligibleTaskClasses: []string{"code"}, MaxTasks: 1, Deadline: now.Add(time.Hour), RollbackTriggers: []optimization.Trigger{{Metric: "error_rate", Threshold: .05, HigherIsWorse: true, Reason: "regression"}}}, gov)
	if err != nil {
		t.Fatal(err)
	}
	rolled, err := runtime.Optimization().Rollback(ctx, canary.ID, "missing guard telemetry")
	if err != nil || rolled.State != optimization.CanaryRolledBack || rolled.RollbackReason == "" {
		t.Fatalf("rollback did not preserve failure evidence: %+v err=%v", rolled, err)
	}
}
