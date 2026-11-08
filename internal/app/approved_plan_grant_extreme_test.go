//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// fixtureRuntimeDecided rewrites an approved record as the engine's reserved
// approved-plan decision. The engine tests cover the reserved decision path
// itself; these are unit fixtures, not native model evidence.
func fixtureRuntimeDecided(t *testing.T, r *Runtime, id string) {
	t.Helper()
	a, err := r.Execution().Engine().ApprovalManager().GetApproval(id)
	if err != nil || a.Status != execution.ApprovalApproved {
		t.Fatalf("fixture approval not approved: %+v %v", a, err)
	}
	a.ApprovedBy = execution.ApprovedPlanDecider
	a.DecisionReason = execution.ApprovedPlanReason
	raw, _ := json.Marshal(a)
	if err := os.WriteFile(filepath.Join(r.ProjectRoot(), statepath.DirName, "approvals", id+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

type approvedPlanChain struct {
	r                   *Runtime
	task                execution.TaskExecution
	pkg                 execution.ConstraintPackage
	canonical           model.Task
	agent               string
	original, transport string
	pendingTransport    execution.RuntimeApproval
	pendingTask         execution.TaskExecution
}

// approvedPlanTransportChain builds the chain the engine produces for an
// approved-plan task: TASK_EXECUTE granted by the runtime, then the exact
// brokered transport request, its recorded proof and runtime decision.
// runtimeOriginal=false decides the TASK_EXECUTE approval as an operator.
func approvedPlanTransportChain(t *testing.T, runtimeOriginal, record bool) approvedPlanChain {
	t.Helper()
	r, task, pkg, canonical, agent := transportFixtureWithTaskApproval(t, true)
	ctx := t.Context()
	task, originalID := fixtureOriginalTaskApproval(t, r, task)
	if runtimeOriginal {
		fixtureRuntimeDecided(t, r, originalID)
	}
	_, pause, err := r.reviewProviderTransport(ctx, task, pkg, canonical, agent, task.AssignedHarness, task.AssignedModel, task.WorktreePath)
	if err != nil || pause == nil || pause.ApprovalReq.OperationType != providerTransportOperation {
		t.Fatalf("transport pause: %v %+v", err, pause)
	}
	run, err := r.Execution().Engine().GetRun(ctx, task.RunID)
	if err != nil {
		t.Fatal(err)
	}
	req := *pause.ApprovalReq
	req.RunID, req.TaskID, req.PlanID, req.PlanVersion = run.RunID, task.TaskID, run.PlanID, run.PlanVersion
	manager := r.Execution().Engine().ApprovalManager()
	transport, err := manager.RequestApproval(req)
	if err != nil {
		t.Fatal(err)
	}
	original, err := manager.GetApproval(originalID)
	if err != nil {
		t.Fatal(err)
	}
	pending := task
	pending.ApprovalID = transport.ApprovalID
	chain := approvedPlanChain{r: r, task: task, pkg: pkg, canonical: canonical, agent: agent, original: originalID, transport: transport.ApprovalID, pendingTransport: *transport, pendingTask: pending}
	if !record {
		return chain
	}
	if err := r.recordApprovedPlanTransport(ctx, "fixture-marshal", run, pending, *original, *transport); err != nil {
		t.Fatalf("exact approved-plan transport refused: %v", err)
	}
	if err := manager.Approve(transport.ApprovalID, "unit-engine", "fixture", transport.CreatedAt); err != nil {
		t.Fatal(err)
	}
	fixtureRuntimeDecided(t, r, transport.ApprovalID)
	chain.task.ApprovalID = transport.ApprovalID
	chain.task.ApprovalRequired = true
	chain.task.Attempts = 2
	chain.pkg.ApprovalRequired = true
	chain.pkg.Digest = execution.ComputeConstraintDigest(chain.pkg)
	run.Tasks[task.TaskID] = chain.task
	if err := r.Execution().Engine().RunStore().UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	return chain
}

func TestExtremeApprovedPlanTransportProofAcceptedThroughProvenance(t *testing.T) {
	c := approvedPlanTransportChain(t, true, true)
	r, task, ctx := c.r, c.task, t.Context()
	// No Marshal launch is live: the runtime proof stands on its own records.
	r.SetProviderTransportReviewSource(func(string, string, string) bool { return false })
	granted, pause, err := r.reviewProviderTransport(ctx, task, c.pkg, c.canonical, c.agent, task.AssignedHarness, task.AssignedModel, task.WorktreePath)
	if err != nil || pause != nil {
		t.Fatalf("runtime approved-plan proof refused: %v %+v", err, pause)
	}
	if handled, err := r.authorizeReviewedProviderTransport(granted, "egress", task.RunID, task.AssignedHarness, task.AssignedModel, c.canonical, c.agent, task.RunID, model.RoleDeveloper, ""); !handled || err != nil {
		t.Fatalf("exact host launch refused: %v %v", handled, err)
	}
	if err := r.consumeReviewedProviderTransport(granted, "", "approved-plan-egress"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{c.original, c.transport} {
		a, _ := r.Execution().Engine().ApprovalManager().GetApproval(id)
		if a.Status != execution.ApprovalConsumed || !execution.IsApprovedPlanGrant(*a) {
			t.Fatalf("chain not consumed one-shot %s: %+v", id, a)
		}
	}
	text, err := r.providerConstraintPrompt(granted, task, c.pkg, task.WorktreePath)
	if err != nil || !strings.Contains(text, "owner's approved-plan rule") || strings.Contains(text, "Marshal-reviewed") || strings.Contains(text, "Hard human approval required") {
		t.Fatalf("approved-plan prompt authority: %v %s", err, text)
	}
	if err := r.consumeReviewedProviderTransport(granted, "", "replay"); err == nil {
		t.Fatal("approved-plan transport replayed")
	}
	run, err := r.Execution().Engine().GetRun(ctx, task.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plans().Handoff(ctx, run.SessionID, projectid.ID(r.ProjectIdentity())); err != nil {
		t.Fatal(err)
	}
	task.State = execution.TaskCompletedPendingVerify
	task.ResultCommit = task.BaseCommit
	run.Tasks[task.TaskID] = task
	run.State = execution.RunDonePendingVerification
	if err := r.Execution().Engine().RunStore().UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	run, _ = r.Execution().Engine().GetRun(ctx, task.RunID)
	mr := marshal.Run{PlanID: run.PlanID, PlanVersion: run.PlanVersion, Process05Bound: true, BaseCommit: task.BaseCommit, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: task.TaskID, Title: task.Description, Files: append([]string(nil), task.TargetFiles...), Worker: task.AssignedHarness, Mode: marshal.Governed, Branch: statepath.RefName + "/fixture-marshal/" + task.TaskID}}}
	if _, err := r.store.SetMarshalRun(ctx, r.ProjectID(), "fixture-marshal", mr, 0); err != nil {
		t.Fatal(err)
	}
	records, err := r.marshalProcess05Provenance(ctx, mr, &run, task)
	if err != nil || len(records) != 1 {
		t.Fatalf("provenance refused approved-plan runtime proof: %v %+v", err, records)
	}
	var p process05ReviewProvenance
	if err := json.Unmarshal([]byte(records[0].Output), &p); err != nil {
		t.Fatal(err)
	}
	if p.Transport.ApprovalID != c.transport || p.Transport.Launch != execution.ApprovedPlanDecider || p.Transport.Owner != approvedPlanProofOwner || p.Binding.Original.ID != c.original {
		t.Fatalf("provenance does not name the runtime grant: %+v", p.Transport)
	}
}

func TestExtremeApprovedPlanTransportRecordRefusesOutsideChain(t *testing.T) {
	for _, name := range []string{"operator-original", "foreign-endpoint", "foreign-run", "next-attempt", "foreign-original"} {
		t.Run(name, func(t *testing.T) {
			c := approvedPlanTransportChain(t, name != "operator-original", false)
			r, ctx := c.r, t.Context()
			run, _ := r.Execution().Engine().GetRun(ctx, c.task.RunID)
			original, _ := r.Execution().Engine().ApprovalManager().GetApproval(c.original)
			task, transport := c.pendingTask, c.pendingTransport
			switch name {
			case "foreign-endpoint":
				var b providerTransportBinding
				_, frozen, _ := strings.Cut(transport.Scope, "exact binding ")
				_ = json.Unmarshal([]byte(frozen), &b)
				b.Endpoint = "198.51.100.7:443"
				req := transportApprovalRequest(b)
				transport.Scope, transport.TargetResource = req.Scope, req.TargetResource
				transport.ActionDigest = execution.ComputeActionDigest(req.OperationType, req.TargetResource, req.DiffPreview, req.Parameters)
				transport.StateDigest = execution.ComputeStateDigest(req.CurrentState)
			case "foreign-run":
				run.RunID = "run-foreign"
			case "next-attempt":
				task.Attempts++
			case "foreign-original":
				original.ApprovalID = "app-foreign"
			}
			if err := r.recordApprovedPlanTransport(ctx, "fixture-marshal", run, task, *original, transport); err == nil {
				t.Fatal("transport outside the exact approved-plan chain recorded a runtime proof")
			}
			if _, err := r.approvedPlanTransportProof(ctx, c.transport); err == nil {
				t.Fatal("refused record still yields a runtime proof")
			}
		})
	}
}

// An operator- or Marshal-decided transport still needs its Marshal-reviewed
// MCP proof, even when a runtime receipt for the same approval exists.
func TestExtremeOperatorDecidedTransportStillRequiresMCPProof(t *testing.T) {
	c := approvedPlanTransportChain(t, true, true)
	r, task, ctx := c.r, c.task, t.Context()
	a, _ := r.Execution().Engine().ApprovalManager().GetApproval(c.transport)
	a.ApprovedBy, a.DecisionReason = "fixture-marshal-model", "ordinary decision"
	raw, _ := json.Marshal(a)
	if err := os.WriteFile(filepath.Join(r.ProjectRoot(), statepath.DirName, "approvals", c.transport+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.approvedPlanTransportProof(ctx, c.transport); err == nil {
		t.Fatal("operator-decided transport accepted the runtime proof")
	}
	if _, pause, err := r.reviewProviderTransport(ctx, task, c.pkg, c.canonical, c.agent, task.AssignedHarness, task.AssignedModel, task.WorktreePath); err == nil || pause != nil {
		t.Fatalf("operator-decided transport admitted without its MCP proof: %v %+v", err, pause)
	}
}

func TestExtremeApprovedPlanExecutionBindsOnlyProcess05BoundRuns(t *testing.T) {
	r := &Runtime{}
	ctx := context.Background()
	p05 := &execution.ExecutionRun{RunID: "run", PlanID: "plan", PlanVersion: 2}
	for name, run := range map[string]marshal.Run{"unbound": {PlanID: "plan", PlanVersion: 2}, "foreign-plan": {PlanID: "other", PlanVersion: 2, Process05Bound: true}, "foreign-version": {PlanID: "plan", PlanVersion: 1, Process05Bound: true}} {
		if r.privateApprovedPlanExecution(ctx, run, p05, "task") != ctx {
			t.Fatalf("%s run bound to an approved plan", name)
		}
	}
	if r.privateApprovedPlanExecution(ctx, marshal.Run{PlanID: "plan", PlanVersion: 2, Process05Bound: true}, p05, "task") == ctx {
		t.Fatal("Process 05 bound run lost its approved-plan binding")
	}
}

func TestExtremeApprovedPlanEngineChainSerial(t *testing.T) { testApprovedPlanEngineChain(t, false) }
func TestExtremeApprovedPlanEngineChainChild(t *testing.T)  { testApprovedPlanEngineChain(t, true) }

// The real engine and owned OpenCode adapter: one bound dispatch of an
// approved-plan task grants TASK_EXECUTE, then the brokered transport, and
// reaches the single owned provider call without any wait or MCP decision.
func testApprovedPlanEngineChain(t *testing.T, child bool) {
	if _, _, err := privateProviderBootstrap("opencode"); err != nil {
		t.Skipf("pinned OpenCode offline bootstrap is not prepared: %v", err)
	}
	r, task, _, _, _ := transportFixtureForProviderMode(t, true, "opencode", true)
	ctx := t.Context()
	engine := r.Execution().Engine()
	run, err := engine.GetRun(ctx, task.RunID)
	if err != nil {
		t.Fatal(err)
	}
	head, err := gitMarshal(ctx, r.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	run.Delivery, run.BaseCommit, task.BaseCommit = execution.DeliveryPreserveBranch, head, head
	task.State, task.ApprovalRequired, task.WorktreePath, task.Attempts = execution.TaskReady, true, "", 0
	if child {
		run.Mode = plan.ModeUltra
		run.Provenance.RepoRoot = r.ProjectRoot()
		task.State = execution.TaskPending
	}
	run.Tasks[task.TaskID] = task
	if err := engine.RunStore().UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if child {
		lane, err := engine.OpenTaskLane(ctx, run.RunID, task.TaskID, head)
		if err != nil {
			t.Fatal(err)
		}
		run = *lane
	}
	// No Marshal launch can review anything: only the approved plan grants.
	r.SetProviderTransportReviewSource(func(string, string, string) bool { return false })
	mr := marshal.Run{PlanID: run.PlanID, PlanVersion: run.PlanVersion, Process05Bound: true, Tasks: []marshal.Task{{PlanTaskID: task.TaskID, Branch: statepath.RefName + "/fixture-marshal/" + task.TaskID}}}
	bound := r.privateApprovedPlanExecution(ctx, mr, &run, task.TaskID)
	final, err := engine.ExecuteTaskBound(bound, run.RunID, task.TaskID, head)
	if err != nil || final.State == execution.RunNeedsApproval || final.BudgetConsumed.ModelCalls != 1 || len(final.Evidence) != 1 {
		t.Fatalf("approved-plan chain did not reach the single owned call in one round: state=%s calls=%d evidence=%d task=%s reason=%q err=%v", final.State, final.BudgetConsumed.ModelCalls, len(final.Evidence), final.Tasks[task.TaskID].State, final.Tasks[task.TaskID].LastFailureReason, err)
	}
	transport, err := engine.ApprovalManager().GetApproval(final.Tasks[task.TaskID].ApprovalID)
	if err != nil || transport.OperationType != providerTransportOperation || transport.Status != execution.ApprovalConsumed || !execution.IsApprovedPlanGrant(*transport) {
		t.Fatalf("transport not runtime-granted and consumed: %+v %v", transport, err)
	}
	proof, err := r.transportReviewProof(ctx, transport.ApprovalID, false)
	if err != nil || !isApprovedPlanTransportProof(proof) || proof.Request.RunID != "fixture-marshal" {
		t.Fatalf("runtime proof absent: %+v %v", proof, err)
	}
	evidence, err := engine.EvidenceOracle().GetEvidence(final.Evidence[0].EvidenceID)
	if err != nil || evidence.ExitCode != 7 {
		t.Fatalf("owned adapter did not validate the rendered authorization: %+v %v", evidence, err)
	}
}
