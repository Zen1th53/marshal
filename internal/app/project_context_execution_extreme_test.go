//go:build !governed

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestActualEngineRequiredContextUsesCurrentOwnedWorktreeAndRefusesMissingMemory(t *testing.T) {
	testActualApprovedInstructionContext(t, "")
}

func TestActualEngineRequiredContextRefusesOversizedInstructionsBeforeProvider(t *testing.T) {
	testActualApprovedInstructionContext(t, strings.Repeat("large mandatory instructions\n", projectcontext.MaximumPacketBytes/16))
}

func TestActualEngineRequiredContextRefusesEscapedWireOverflowBeforeProvider(t *testing.T) {
	testActualApprovedInstructionContext(t, strings.Repeat("\"\\", projectcontext.MaximumPacketBytes/3))
}

func testActualApprovedInstructionContext(t *testing.T, oversized string) {
	t.Helper()
	client := &requiredOwnedFixtureAdapter{provider: "codex"}
	r, b, record, _ := requiredOwnedContextFixture(t, client)
	request := planCreateRequest()
	request.SessionID, request.ProjectID = b.Session, projectid.ID(r.ProjectIdentity())
	request.Tasks[0].Mutating = true
	capacity := goalintake.UnknownCapacity("codex", true)
	request.Candidates = []goalintake.Candidate{{Provider: "codex", Model: "test-model", Capacity: capacity, Governance: constitution.GovernanceVerified}}
	request.HarnessCandidates = []plan.HarnessCandidate{{Profile: model.HarnessProfile{Harness: "codex", InstalledVersion: "1", SupportedModels: []string{"test-model"}, DefaultModel: "test-model", ProbeEvidenceID: "owned-host-instructions", ProbedAt: time.Now().UTC()}, InstalledVersion: "1", Provider: "codex", Capacity: capacity}}
	const approvedInstructions = "Write marker/worker.txt with exactly worker-ok\n. Preserve the owner scope.\n"
	const approvedOutput = "marker/worker.txt exact bytes"
	request.Tasks[0].Instructions, request.Tasks[0].ExpectedOutput = approvedInstructions, approvedOutput
	if oversized != "" {
		request.Tasks[0].Instructions = oversized
	}
	if _, err := r.Plans().Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plans().Approve(t.Context(), request.ProjectID); err != nil {
		t.Fatal(err)
	}
	const agentID = "AGENT-actual-required"
	if err := r.store.RegisterAgent(t.Context(), model.Agent{ID: agentID, ProjectID: r.ProjectID(), DisplayName: "Actual governed fixture", Role: model.RoleDeveloper, ModelProvider: "codex", ModelName: "configured-selector", Status: model.AgentRegistered, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	service := r.Execution()
	started := make(chan execution.TaskExecution, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	var actualRunID string
	client.handle = func(_ context.Context, req adapter.Request) (adapter.Result, error) {
		if !strings.Contains(req.TrustedContext, approvedInstructions) || !strings.Contains(req.TrustedContext, approvedOutput) {
			return adapter.Result{}, errors.New("actual owned provider omitted approved instructions/output")
		}
		current, err := service.Engine().RunStore().GetRun(t.Context(), actualRunID)
		if err != nil {
			return adapter.Result{}, err
		}
		started <- current.Tasks["fix"]
		<-release
		return adapter.Result{}, errors.New("intentional owned host capture stop")
	}
	run, err := service.StartRun(t.Context(), b.Session, request.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	actualRunID = run.RunID
	if err := service.SetPreserveBranch(t.Context(), run.RunID, b.Base); err != nil {
		t.Fatal(err)
	}
	executionErr := make(chan error, 1)
	go func() {
		defer close(done)
		current, err := service.Engine().ExecuteRun(t.Context(), run.RunID)
		if err == nil && current.State == execution.RunNeedsApproval {
			for _, pending := range current.Tasks {
				if pending.ApprovalID != "" {
					err = service.Approve(t.Context(), pending.ApprovalID, "fixture-owner", "Mock harness fixture only")
					if err != nil {
						break
					}
				}
			}
			if err == nil {
				_, err = service.Engine().ExecuteRun(t.Context(), run.RunID)
			}
		}
		executionErr <- err
	}()
	defer func() {
		close(release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("governed fixture did not settle")
		}
	}()
	if oversized != "" {
		select {
		case err := <-executionErr:
			current, readErr := service.Engine().GetRun(t.Context(), run.RunID)
			if readErr != nil || err == nil || !strings.Contains(current.Tasks["fix"].LastFailureReason, projectcontext.ErrBudget.Error()) || client.probes.Load() != 0 || client.runs.Load() != 0 || current.Tasks["fix"].ResultCommit != "" {
				t.Fatalf("over-budget approved instructions reached provider: probes=%d runs=%d err=%v read=%v", client.probes.Load(), client.runs.Load(), err, readErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("over-budget approved instructions did not refuse actual dispatch")
		}
		return
	}
	var task execution.TaskExecution
	select {
	case task = <-started:
	case err := <-executionErr:
		current, _ := service.Engine().GetRun(t.Context(), run.RunID)
		t.Fatalf("actual owned provider returned before callback: %v %+v", err, current)
	case <-time.After(5 * time.Second):
		t.Fatal("actual owned provider callback did not start")
	}
	if client.probes.Load() != 1 || client.runs.Load() != 1 {
		t.Fatal("fixture bypassed actual owned provider callback")
	}
	text, receipt, err := r.requiredExecutionContext(t.Context(), task, agentID, task.AssignedHarness, task.AssignedModel, "Exact provider instructions\n", 16384, "dispatch")
	if err != nil || !strings.Contains(text, record.Body) || receipt.Binding.RunID != task.RunID || receipt.Binding.Principal != agentID || receipt.Binding.Model != task.AssignedModel || receipt.RequiredCount != 1 || receipt.IncludedCount != 1 {
		t.Fatalf("actual required context missing: %+v %v", receipt, err)
	}
	if !strings.Contains(text, approvedInstructions) || !strings.Contains(text, approvedOutput) {
		t.Fatal("canonical approved task instructions/output were omitted from actual owned provider admission")
	}
	if err := r.preflightRequiredProcess05Context(t.Context(), task, agentID, task.AssignedHarness, task.AssignedModel, execution.ConstraintPackage{GoalOutcome: "Preserve history", TaskID: task.TaskID}); err != nil {
		t.Fatal(err)
	}
	requestToProvider := adapter.Request{TaskID: task.CanonicalTaskID, Title: task.Description, Model: task.AssignedModel, Worktree: task.WorktreePath, TrustedContext: "Exact provider instructions\n"}
	providerCtx, finishContext, err := r.prepareWatchedRequiredProcess05Request(t.Context(), task, agentID, task.AssignedHarness, task.AssignedModel, &requestToProvider, strings.Repeat("optional source metadata\n", 16384))
	if err != nil {
		t.Fatal(err)
	}
	defer finishContext()
	wire, err := requiredProcess05ProviderPrompt(task.AssignedHarness, requestToProvider)
	if err != nil || len(wire) > 16384 || !strings.Contains(requestToProvider.TrustedContext, record.Body) || strings.Contains(requestToProvider.TrustedContext, "optional source metadata") {
		t.Fatal("provider envelope omitted required context or retained excess optional metadata")
	}
	for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
		t.Run("wire "+provider, func(t *testing.T) {
			wire, err := requiredProcess05ProviderPrompt(provider, requestToProvider)
			if err != nil || !strings.Contains(string(wire), "marker/worker.txt") || !strings.Contains(requestToProvider.TrustedContext, approvedInstructions) || !strings.Contains(requestToProvider.TrustedContext, approvedOutput) {
				t.Fatal("actual governed provider encoder omitted approved instructions/output")
			}
		})
	}
	for _, scenario := range []string{"foreign principal", "wrong model", "wrong workspace", "wrong canonical task"} {
		t.Run(scenario, func(t *testing.T) {
			current, principal, modelName := task, agentID, task.AssignedModel
			switch scenario {
			case "foreign principal":
				principal = "other"
			case "wrong model":
				modelName = "other-model"
			case "wrong workspace":
				current.WorktreePath = t.TempDir()
			case "wrong canonical task":
				current.CanonicalTaskID = "TASK-foreign"
			}
			if text, _, err := r.requiredExecutionContext(t.Context(), current, principal, task.AssignedHarness, modelName, "", 16384, "dispatch"); err == nil || text != "" {
				t.Fatal("foreign execution binding admitted")
			}
		})
	}
	// Authorized worktree writes must not themselves invalidate mandatory
	// context. The watcher checks canonical memory and authority, not live Tree.
	if err := os.WriteFile(filepath.Join(task.WorktreePath, "owned-provider-work.txt"), []byte("authorized mock work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-providerCtx.Done():
		t.Fatal("owned worktree write invalidated required context")
	case <-time.After(300 * time.Millisecond):
	}
	if _, err = r.store.UpdateMemory(t.Context(), record.ProjectID, record.ID, record.Revision, func(m *model.MemoryRecordV2) error { m.ACLScope = "foreign"; return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-providerCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("live required context revocation did not cancel owned provider context")
	}
	if err := finishContext(); err == nil {
		t.Fatal("revoked required context did not quarantine output")
	}
	if text, _, err = r.requiredExecutionContext(t.Context(), task, agentID, task.AssignedHarness, task.AssignedModel, "", 16384, "dispatch"); err == nil || text != "" {
		t.Fatal("missing authorized memory reached next invocation")
	}

}
