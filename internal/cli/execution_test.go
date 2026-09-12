package cli

import (
	"context"
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

func TestExecCLI_Usage(t *testing.T) {
	dir := gitDirectory(t)
	_, errOut, code := runCLI(t, dir, "exec")
	if code == 0 {
		t.Fatalf("expected non-zero exit code for 'marshal exec' with no args")
	}
	if !strings.Contains(errOut, "Usage: marshal exec") {
		t.Fatalf("expected usage message in stderr, got: %s", errOut)
	}

	_, errOut, code = runCLI(t, dir, "exec", "invalid-subcommand")
	if code == 0 {
		t.Fatalf("expected non-zero exit code for invalid subcommand")
	}
	if !strings.Contains(errOut, "Usage: marshal exec") {
		t.Fatalf("expected usage message in stderr, got: %s", errOut)
	}
}

func TestExecCLI_Lifecycle(t *testing.T) {
	ctx := context.Background()
	dir := gitDirectory(t)

	// 1. Bootstrap MARSHAL in this repo
	if _, err := app.Bootstrap(ctx, dir); err != nil {
		t.Fatalf("bootstrap project: %v", err)
	}

	runtime, err := app.Open(ctx, dir)
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}

	sessionID := "SESSION-cli-exec"
	projectID := projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")

	// 2. Set up Goal contract
	goal := model.GoalContract{
		ID:                  "GOAL-cli",
		SessionID:           sessionID,
		Revision:            1,
		ProjectID:           string(projectID),
		OriginalRequest:     "Update the test README.",
		ConstitutionVersion: constitution.Current.String(),
		Confirmation:        model.ConfirmationApproved,
		DesiredOutcome:      "README is updated",
		ExpectedArtifact:    "README.md",
		SuccessCriteria:     []string{"README is updated"},
		Risk:                model.R1,
		AuthoritySource:     "operator",
	}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("save goal: %v", err)
	}

	// 3. Create & Approve Plan
	now := time.Now().UTC()
	cap := goalintake.UnknownCapacity("openai", true)
	createReq := app.CreatePlanRequest{
		SessionID: sessionID,
		ProjectID: projectID,
		Tasks: []plan.Task{
			{
				ID:       "task-1",
				Title:    "update readme",
				Mutating: true,
				Paths:    []string{"README.md"},
				Criteria: []string{"README is updated"},
			},
		},
		Candidates: []goalintake.Candidate{
			{Provider: "openai", Model: "gpt-4o", Capacity: cap, Governance: constitution.GovernanceVerified},
		},
		HarnessCandidates: []plan.HarnessCandidate{
			{
				Profile: model.HarnessProfile{
					Harness:          "mock-harness",
					InstalledVersion: "1.0.0",
					SupportedModels:  []string{"gpt-4o"},
					DefaultModel:     "gpt-4o",
					ProbeEvidenceID:  "EV-mock",
					ProbedAt:         now,
				},
				InstalledVersion: "1.0.0",
				Provider:         "openai",
				Capacity:         cap,
			},
		},
	}
	if _, err := runtime.Plans().Create(ctx, createReq); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if _, err := runtime.Plans().Approve(ctx, projectID); err != nil {
		t.Fatalf("approve plan: %v", err)
	}

	// Close runtime so CLI commands can open it cleanly
	_ = runtime.Close()

	// 4. CLI: marshal exec start --session <session> --project <project>
	stdout, stderr, code := runCLI(t, dir, "exec", "start", "--session", sessionID, "--project", string(projectID))
	if code != 0 {
		t.Fatalf("exec start failed (code %d): %s\n%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "run=") {
		t.Fatalf("expected run ID in stdout, got: %s", stdout)
	}

	// Extract runID from stdout line "run=RUN-... state=READY tasks=1"
	var runID string
	for _, part := range strings.Fields(stdout) {
		if strings.HasPrefix(part, "run=") {
			runID = strings.TrimPrefix(part, "run=")
			break
		}
	}
	if runID == "" {
		t.Fatalf("failed to extract runID from stdout: %s", stdout)
	}

	// 5. CLI: marshal exec status <runID>
	stdout, stderr, code = runCLI(t, dir, "exec", "status", runID)
	if code != 0 {
		t.Fatalf("exec status failed: %s", stderr)
	}
	if !strings.Contains(stdout, runID) {
		t.Fatalf("expected runID %s in status output, got: %s", runID, stdout)
	}

	// 6. CLI: marshal --json exec status <runID>
	stdout, stderr, code = runCLI(t, dir, "--json", "exec", "status", runID)
	if code != 0 {
		t.Fatalf("exec status --json failed: %s", stderr)
	}
	if !strings.Contains(stdout, `"run_id":`) {
		t.Fatalf("expected json output for exec status, got: %s", stdout)
	}

	// 7. Test rollback with baseline checkpoint
	reopenedRuntime, err := app.Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen runtime: %v", err)
	}
	runRecord, err := reopenedRuntime.Execution().GetRun(ctx, runID)
	_ = reopenedRuntime.Close()
	if err != nil {
		t.Fatalf("get run record: %v", err)
	}

	if len(runRecord.Checkpoints) > 0 {
		cpID := runRecord.Checkpoints[0].CheckpointID
		stdout, stderr, code = runCLI(t, dir, "exec", "rollback", cpID)
		if code != 0 {
			t.Fatalf("exec rollback failed: %s", stderr)
		}
		if !strings.Contains(stdout, "restored=true") {
			t.Fatalf("expected restored=true in rollback stdout, got: %s", stdout)
		}
	}

	// 8. Register mock harness on runtime to test execution through CLI
	rtForExec, err := app.Open(ctx, dir)
	if err != nil {
		t.Fatalf("open runtime for exec: %v", err)
	}
	rtForExec.Execution().RegisterHarness(execution.NewMockHarness("mock-harness", func(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
		return execution.TaskResult{
			TaskID:  task.TaskID,
			Success: true,
			Claims: []execution.ExecutionClaim{
				{
					ClaimID:      "claim-" + task.TaskID,
					TaskID:       task.TaskID,
					ClaimText:    "updated readme",
					Status:       execution.ClaimSupported,
					EvidenceRefs: []string{"ev-1"},
				},
			},
		}, nil
	}))
	rtForExec.Execution().Engine().EvidenceOracle().RecordEvidence(execution.ExecutionEvidence{
		EvidenceID:    "ev-1",
		RunID:         runID,
		RelevantFiles: []string{"README.md"},
		Status:        execution.EvidenceValid,
	})

	// Execute through service
	execRun, err := rtForExec.Execution().ExecuteRun(ctx, runID)
	if err != nil {
		t.Fatalf("service ExecuteRun: %v", err)
	}

	// Handle approval if gated
	if execRun.State == execution.RunNeedsApproval {
		var appID string
		for _, tsk := range execRun.Tasks {
			if tsk.ApprovalID != "" {
				appID = tsk.ApprovalID
				break
			}
		}
		if appID != "" {
			_ = rtForExec.Close()
			// CLI: marshal exec approve <approval-id> --decider lead --reason "LGTM"
			stdout, stderr, code = runCLI(t, dir, "exec", "approve", appID, "--decider", "lead", "--reason", "LGTM")
			if code != 0 {
				t.Fatalf("exec approve failed: %s", stderr)
			}
			if !strings.Contains(stdout, "status=APPROVED") {
				t.Fatalf("expected status=APPROVED in approve output, got: %s", stdout)
			}

			// Resume execution
			rtForExec, err = app.Open(ctx, dir)
			if err != nil {
				t.Fatalf("reopen runtime: %v", err)
			}
			rtForExec.Execution().RegisterHarness(execution.NewMockHarness("mock-harness", func(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (execution.TaskResult, error) {
				return execution.TaskResult{
					TaskID:  task.TaskID,
					Success: true,
					Claims: []execution.ExecutionClaim{
						{
							ClaimID:      "claim-" + task.TaskID,
							TaskID:       task.TaskID,
							ClaimText:    "updated readme",
							Status:       execution.ClaimSupported,
							EvidenceRefs: []string{"ev-1"},
						},
					},
				}, nil
			}))
			execRun, err = rtForExec.Execution().ExecuteRun(ctx, runID)
			if err != nil {
				t.Fatalf("ExecuteRun after approve: %v", err)
			}
		}
	}

	if execRun.State != execution.RunDonePendingVerification {
		t.Fatalf("expected run to reach RunDonePendingVerification, got %s", execRun.State)
	}
	_ = rtForExec.Close()

	// 9. CLI: marshal exec handoff <runID>
	stdout, stderr, code = runCLI(t, dir, "exec", "handoff", runID)
	if code != 0 {
		t.Fatalf("exec handoff failed: %s", stderr)
	}
	if !strings.Contains(stdout, "bundle=") || !strings.Contains(stdout, "DONE_PENDING_VERIFICATION") {
		t.Fatalf("expected handoff bundle summary in stdout, got: %s", stdout)
	}
}
