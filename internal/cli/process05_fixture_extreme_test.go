//go:build !governed

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

const process05FixturePolicy = "Preserve every original CLI and Process05 assertion and its accepted evidence."

// Only the adapter transport is simulated. Runtime construction captures its
// real Codex harness before any registration; canonical admission, approvals,
// complete required memory and the source watcher remain production paths.
type process05OwnedFixtureAdapter struct {
	runtime      *app.Runtime
	probes, runs atomic.Int64
	handler      func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error)
}

func (a *process05OwnedFixtureAdapter) Probe(context.Context) (adapter.Probe, error) {
	a.probes.Add(1)
	return adapter.Probe{Name: "codex", Available: true, Version: "1.0.0"}, nil
}
func (*process05OwnedFixtureAdapter) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusSuccess, nil
}
func (a *process05OwnedFixtureAdapter) Resume(ctx context.Context, _ string, req adapter.Request) (adapter.Result, error) {
	return a.Run(ctx, req)
}
func (*process05OwnedFixtureAdapter) Capabilities() map[string]string {
	return map[string]string{"adapter": "owned-process05-fixture"}
}
func (*process05OwnedFixtureAdapter) CollectEvidence(adapter.Result) map[string]any { return nil }
func (*process05OwnedFixtureAdapter) Shutdown(context.Context, string) error        { return nil }
func (a *process05OwnedFixtureAdapter) Run(ctx context.Context, req adapter.Request) (adapter.Result, error) {
	a.runs.Add(1)
	if a.handler == nil || !strings.Contains(req.TrustedContext, process05FixturePolicy) || !strings.Contains(req.TrustedContext, "Preserve governed CLI and Process05 execution") {
		return adapter.Result{}, errors.New("owned callback omitted accepted full project context")
	}
	// The fixture stands for a launched provider process: acknowledge launch
	// so the dispatch barrier releases the run lock and store connection
	// before the callback reads canonical state.
	execution.AcknowledgeWorkerLaunch(ctx)
	runs, err := a.runtime.Execution().ListRuns(ctx)
	if err != nil {
		return adapter.Result{}, err
	}
	var selected execution.TaskExecution
	var current execution.ExecutionRun
	count := 0
	for _, run := range runs {
		for _, task := range run.Tasks {
			if task.State == execution.TaskRunning && task.CanonicalTaskID == req.TaskID && task.WorktreePath == req.Worktree {
				selected, current = task, run
				count++
			}
		}
	}
	if count != 1 {
		return adapter.Result{}, fmt.Errorf("owned callback selected %d current tasks", count)
	}
	goal, err := a.runtime.Store().GetGoalContract(ctx, current.GoalID, current.GoalRevision)
	if err != nil {
		return adapter.Result{}, err
	}
	approved, err := a.runtime.Store().GetPlan(ctx, current.PlanID, current.PlanVersion)
	if err != nil {
		return adapter.Result{}, err
	}
	// This package adapts existing fixture assertions only, never supplies
	// authority. Check its values against the actually observed provider header.
	pkg, err := execution.BuildConstraintPackage(goal, selected, approved)
	if err != nil {
		return adapter.Result{}, err
	}
	expected := []string{"GOAL: " + pkg.GoalOutcome + "\n", "ASSIGNED TASK: " + selected.TaskID + " - " + pkg.TaskDescription + "\n", "SECURITY DIGEST: "}
	expected = append(expected, pkg.HardConstraints...)
	expected = append(expected, pkg.DoNotDo...)
	expected = append(expected, pkg.VerificationObligations...)
	for _, text := range expected {
		if !strings.Contains(req.TrustedContext, text) {
			return adapter.Result{}, fmt.Errorf("actual owned prompt omitted canonical constraint %q", text)
		}
	}
	result, err := a.handler(ctx, selected, pkg, req.Worktree)
	if err != nil {
		return adapter.Result{}, err
	}
	if !result.Success {
		return adapter.Result{}, errors.New(result.ErrorMessage)
	}
	now := time.Now().UTC()
	return adapter.Result{Adapter: "codex", AdapterVersion: "1.0.0", Model: req.Model, SessionID: "owned-process05-fixture", Status: adapter.StatusSuccess, Stdout: []byte("Owned fixture completed " + selected.TaskID), StartedAt: now, EndedAt: now}, nil
}

func openProcess05FixtureRuntime(t *testing.T, ctx context.Context, dir string) (*app.Runtime, func(string, func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error)), projectid.ID) {
	t.Helper()
	installProcess05FixtureTools(t)
	client := &process05OwnedFixtureAdapter{}
	r, err := app.OpenWithOptions(ctx, dir, app.Options{Adapters: map[string]adapter.Adapter{"codex": client}})
	if err != nil {
		t.Fatal(err)
	}
	client.runtime = r
	accepted, err := r.Store().ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if errors.Is(err, projectcontext.ErrReviewPending) {
		acceptProcess05FixtureContract(t, r)
	} else if err != nil {
		t.Fatal(err)
	} else if accepted.Contract.Purpose != "Preserve governed CLI and Process05 execution" {
		t.Fatal("reopened fixture changed accepted contract")
	}
	// Reopens must retain the same durable worker identity. Creating another
	// eligible worker would make the actual provider callback ambiguous.
	agents, err := r.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	eligible := 0
	for _, agent := range agents {
		if agent.ProjectID == r.ProjectID() && agent.Role == model.RoleDeveloper && agent.ModelProvider == "codex" && agent.Status != model.AgentDisabled {
			if agent.DisplayName != "Owned Process05 fixture worker" {
				t.Fatal("reopened fixture acquired a foreign worker")
			}
			eligible++
		}
	}
	if eligible == 0 {
		if _, err := r.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "Owned Process05 fixture worker", Role: model.RoleDeveloper, ModelProvider: "codex"}); err != nil {
			t.Fatal(err)
		}
	} else if eligible != 1 {
		t.Fatalf("fixture worker identity is ambiguous: %d", eligible)
	}
	register := func(_ string, handler func(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error)) {
		client.handler = handler
	}
	return r, register, projectid.ID(r.ProjectIdentity())
}

func prepareProcess05FixturePlan(req app.CreatePlanRequest) app.CreatePlanRequest {
	capacity := goalintake.UnknownCapacity("codex", true)
	for i := range req.Candidates {
		req.Candidates[i].Provider, req.Candidates[i].Capacity = "codex", capacity
	}
	for i := range req.HarnessCandidates {
		req.HarnessCandidates[i].Profile.Harness = "codex"
		req.HarnessCandidates[i].Provider, req.HarnessCandidates[i].Capacity = "codex", capacity
	}
	for i := range req.Tasks {
		req.Tasks[i].Instructions = "Complete only the approved task: " + req.Tasks[i].Title
	}
	return req
}

// Genuine immutable acceptance uses the separate protected proposal/Decision
// APIs. It does not synthesize a Source, packet admission or invocation grant.
func acceptProcess05FixtureContract(t *testing.T, r *app.Runtime) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	st := r.Store()
	if err := st.InitMCPProposals(ctx); err != nil {
		t.Fatal(err)
	}
	rec := model.MemoryRecordV2{ID: "MEM-process05-fixture", ProjectID: r.ProjectID(), Revision: 1, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Confidence: model.ConfidenceVerified, Title: "Preserve governed assertions", Body: process05FixturePolicy, Scope: "project", ScopeID: r.ProjectID(), ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: "owner"}}
	rec.ContentDigest = rec.CanonicalDigest()
	if err := st.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}
	audience := store.ProjectContextAudience{Actor: "owner", ScopeIDs: []string{r.ProjectID()}}
	_, inventory, err := st.ProjectContractInventory(ctx, r.ProjectContractBinding(), audience)
	if err != nil {
		t.Fatal(err)
	}
	c := projectcontext.Contract{FormatVersion: 1, Project: r.ProjectContractBinding(), ID: "CONTRACT-process05-fixture", Revision: 1, Purpose: "Preserve governed CLI and Process05 execution", Components: []projectcontext.Component{{ID: "core", Responsibility: "Governed application", Paths: []string{"README.md", "pkg", "docs", "fail.txt"}}}, Invariants: []string{"Preserve original source and accepted audit evidence"}, ReviewedInventoryDigest: inventory, Required: []projectcontext.MemoryRef{{ID: rec.ID, ProjectID: rec.ProjectID, Scope: rec.Scope, ScopeID: rec.ScopeID, Revision: rec.Revision, ContentDigest: rec.ContentDigest, StateFingerprint: projectcontext.MemoryFingerprint(rec), Class: projectcontext.Policy, Applies: projectcontext.Applicability{ProjectWide: true}, Reason: "Owner-reviewed complete fixture policy", AcceptanceOccurrence: "owner-process05-policy"}}}
	source, _, err := st.AdmitMCPProjectContract(ctx, store.MCPProposal{ID: "mcp:process05-contract", Launch: "process05-fixture-launch", Owner: "owner", Provider: "codex", Submission: "process05-contract"}, store.ProjectContractSubmission{Action: "project-contract", Contract: c})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := c.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	binding, err := json.Marshal(store.ProjectContractApprovalBinding{ProposalDigest: source.Digest, ContractDigest: digest, InventoryDigest: inventory})
	if err != nil {
		t.Fatal(err)
	}
	request := permission.Request{Kind: "project-contract", Object: c.ID, Scope: r.ProjectID(), Who: "Marshal", Reason: "Review exact Process05 fixture context", ProposalID: source.ID, ApprovalBinding: string(binding), SourceLaunch: source.Launch, SourceOccurrence: "mcp:process05-decision"}
	raw, err := json.Marshal(map[string]any{"action": "decide", "id": "QUESTION-process05", "allow": true, "reason": "Reviewed exact fixture contract"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = st.AdmitMCPProposal(ctx, r.ProjectID(), store.MCPProposal{ID: request.SourceOccurrence, Launch: source.Launch, Owner: source.Owner, Provider: source.Provider, Submission: "process05-decision", Payload: string(raw)}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.AcceptMCPDecision(ctx, r.ProjectID(), request.SourceOccurrence, string(encoded), request.Key()); err != nil {
		t.Fatal(err)
	}
	if err = st.SetMCPProposalState(ctx, r.ProjectID(), request.SourceOccurrence, "applying", "protected fixture acceptance"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.CommitMCPProjectContractDecision(ctx, request, true, audience, now, model.Event{ID: "process05-accepted", ProjectID: r.ProjectID(), Type: "PERMISSION_DECIDED", Timestamp: now, Data: map[string]any{"source_occurrence": request.SourceOccurrence}}); err != nil {
		t.Fatal(err)
	}
}
