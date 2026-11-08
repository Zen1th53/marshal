//go:build !governed

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
)

// Owner rule (Extreme only): the approved plan already decided its own task
// scope, so its brokered transport is granted by the runtime instead of a
// Marshal-reviewed MCP decision. The runtime proof below stands in for that
// review only for the reserved runtime decider and only when the TASK_EXECUTE
// approval in the same chain was itself granted by the approved plan.
const (
	approvedPlanProofOwner    = "owner-approved-plan"
	approvedPlanProofProvider = "runtime"
)

var errApprovedPlanProof = errors.New("approved-plan transport grant differs")

func approvedPlanTransportEvent(approvalID string) string {
	return "approved-plan-transport-" + approvalID
}

// privateApprovedPlanExecution binds the dispatch of a Process 05 bound
// Marshal run to its approved plan. The engine re-checks the plan; this
// binding alone grants nothing.
func (r *Runtime) privateApprovedPlanExecution(ctx context.Context, run marshal.Run, p05 *execution.ExecutionRun, taskID string) context.Context {
	if !run.Process05Bound || p05 == nil || taskID == "" || p05.PlanID != run.PlanID || p05.PlanVersion != run.PlanVersion {
		return ctx
	}
	marshalID := marshalRunID(run)
	return execution.WithApprovedPlanExecution(ctx, execution.ApprovedPlanExecution{MarshalRun: marshalID, Run: p05.RunID, Task: taskID, Plan: run.PlanID, PlanVersion: run.PlanVersion, Transport: func(ctx context.Context, current execution.ExecutionRun, task execution.TaskExecution, original, transport execution.RuntimeApproval) error {
		return r.recordApprovedPlanTransport(ctx, marshalID, current, task, original, transport)
	}})
}

// recordApprovedPlanTransport checks that a pending transport request is the
// exact brokered provider transport of this run/task/plan/attempt, chained to
// the approved-plan TASK_EXECUTE grant, and durably records the runtime proof.
func (r *Runtime) recordApprovedPlanTransport(ctx context.Context, marshalID string, run execution.ExecutionRun, task execution.TaskExecution, original, transport execution.RuntimeApproval) error {
	if !execution.IsApprovedPlanGrant(original) || original.OperationType != "TASK_EXECUTE" || original.ExpiresAt == nil || transport.Status != execution.ApprovalRequested || transport.OperationType != providerTransportOperation {
		return errApprovedPlanProof
	}
	_, frozen, framed := strings.Cut(transport.Scope, "exact binding ")
	var b providerTransportBinding
	if !framed || json.Unmarshal([]byte(frozen), &b) != nil || transport.Scope != transportApprovalRequest(b).Scope || b.Version != 1 || !validProviderTransportEndpoints(b) {
		return errApprovedPlanProof
	}
	req := transportApprovalRequest(b)
	if transport.TargetResource != req.TargetResource || transport.RiskLevel != model.R3 || transport.ActionDigest != execution.ComputeActionDigest(req.OperationType, req.TargetResource, req.DiffPreview, req.Parameters) || transport.StateDigest != execution.ComputeStateDigest(req.CurrentState) {
		return errApprovedPlanProof
	}
	if b.Project != r.ProjectIdentity() || string(run.ProjectID) != b.Project || b.Run != run.RunID || b.Task != task.TaskID || b.CanonicalTask != task.CanonicalTaskID || b.Plan != run.PlanID || b.PlanVersion != run.PlanVersion || b.Provider != task.AssignedHarness || b.Model != task.AssignedModel || b.Worktree != filepath.Clean(task.WorktreePath) || b.Base != task.BaseCommit || b.AgentRole != task.AssignedRole || b.AssignedAgent != task.AssignedAgent || b.Attempt != task.Attempts+1 {
		return errApprovedPlanProof
	}
	if !b.Original.Required || b.Original.ID != original.ApprovalID || b.Original.Action != original.ActionDigest || b.Original.State != original.StateDigest || !b.Original.Expires.Equal(*original.ExpiresAt) {
		return errApprovedPlanProof
	}
	required, err := r.transportTaskApprovalRequired(ctx, run, task)
	if err != nil || !required {
		return errApprovedPlanProof
	}
	planDigest, err := r.transportPlanDigest(ctx, run, task)
	if err != nil || planDigest != b.PlanDigest {
		return errApprovedPlanProof
	}
	binding, err := json.Marshal(transport)
	if err != nil {
		return err
	}
	data := map[string]any{"approval_id": transport.ApprovalID, "task_approval_id": original.ApprovalID, "run_id": run.RunID, "task_id": task.TaskID, "plan_id": run.PlanID, "plan_version": strconv.FormatInt(run.PlanVersion, 10), "marshal_run": marshalID, "approval_binding": string(binding), "decider": execution.ApprovedPlanDecider, "rule": execution.ApprovedPlanReason}
	return r.store.AppendEvent(ctx, nil, model.Event{ID: approvedPlanTransportEvent(transport.ApprovalID), Type: execution.ApprovedPlanEvent, ProjectID: r.ProjectID(), Timestamp: time.Now().UTC(), Data: data})
}

// approvedPlanTransportProof rebuilds the runtime proof for a transport
// approval the runtime granted. The receipt is re-checked against both
// current approval records; an operator- or Marshal-decided approval never
// matches, so it still requires its Marshal-reviewed MCP proof.
func (r *Runtime) approvedPlanTransportProof(ctx context.Context, approvalID string) (store.MCPTransportReview, error) {
	refuse := func() (store.MCPTransportReview, error) { return store.MCPTransportReview{}, errApprovedPlanProof }
	event := approvedPlanTransportEvent(approvalID)
	body, err := r.store.ApprovedPlanGrantRecord(ctx, r.ProjectID(), event, execution.ApprovedPlanEvent)
	if err != nil {
		return refuse()
	}
	var data map[string]string
	if json.Unmarshal([]byte(body), &data) != nil || len(data) != 10 {
		return refuse()
	}
	canonical, _ := json.Marshal(data)
	if !bytes.Equal(canonical, []byte(body)) || data["approval_id"] != approvalID || data["decider"] != execution.ApprovedPlanDecider || data["rule"] != execution.ApprovedPlanReason || data["marshal_run"] == "" {
		return refuse()
	}
	manager := r.Execution().Engine().ApprovalManager()
	a, err := manager.GetApproval(approvalID)
	if err != nil || !execution.IsApprovedPlanGrant(*a) || a.OperationType != providerTransportOperation || a.RunID != data["run_id"] || a.TaskID != data["task_id"] || a.PlanID != data["plan_id"] || strconv.FormatInt(a.PlanVersion, 10) != data["plan_version"] || a.ExpiresAt == nil {
		return refuse()
	}
	var reviewed execution.RuntimeApproval
	if strictProvenanceJSON(data["approval_binding"], &reviewed) != nil || reviewed.Status != execution.ApprovalRequested || reviewed.ApprovalID != a.ApprovalID || reviewed.OperationType != a.OperationType || reviewed.Scope != a.Scope || reviewed.TargetResource != a.TargetResource || reviewed.RiskLevel != a.RiskLevel || reviewed.ActionDigest != a.ActionDigest || reviewed.StateDigest != a.StateDigest || reviewed.RunID != a.RunID || reviewed.TaskID != a.TaskID || reviewed.PlanID != a.PlanID || reviewed.PlanVersion != a.PlanVersion || !reviewed.CreatedAt.Equal(a.CreatedAt) || reviewed.ExpiresAt == nil || !reviewed.ExpiresAt.Equal(*a.ExpiresAt) {
		return refuse()
	}
	at := strings.LastIndex(reviewed.Scope, "exact binding ")
	if at < 0 {
		return refuse()
	}
	var b providerTransportBinding
	if json.Unmarshal([]byte(reviewed.Scope[at+len("exact binding "):]), &b) != nil || reviewed.Scope != transportApprovalRequest(b).Scope || b.Version != 1 || !validProviderTransportEndpoints(b) || !b.Original.Required || b.Original.ID != data["task_approval_id"] || b.Run != a.RunID || b.Task != a.TaskID || b.Plan != a.PlanID || b.PlanVersion != a.PlanVersion {
		return refuse()
	}
	o, err := manager.GetApproval(b.Original.ID)
	if err != nil || !execution.IsApprovedPlanGrant(*o) || o.OperationType != "TASK_EXECUTE" || o.RunID != a.RunID || o.TaskID != a.TaskID || o.PlanID != a.PlanID || o.PlanVersion != a.PlanVersion || o.ActionDigest != b.Original.Action || o.StateDigest != b.Original.State || o.ExpiresAt == nil || !o.ExpiresAt.Equal(b.Original.Expires) {
		return refuse()
	}
	sum := sha256.Sum256([]byte(body))
	source := store.MCPProposal{ID: event, Launch: execution.ApprovedPlanDecider, Owner: approvedPlanProofOwner, Provider: approvedPlanProofProvider, Digest: hex.EncodeToString(sum[:]), Payload: body, State: "applied"}
	request := permission.Request{Kind: "marshal-command", Object: "/marshal approve-task " + approvalID, RunID: data["marshal_run"], Scope: "runtime approved-plan grant", ApprovalBinding: data["approval_binding"], SourceLaunch: source.Launch, SourceOccurrence: source.ID}
	return store.MCPTransportReview{Source: source, Request: request}, nil
}

// transportReviewProof returns the review behind an exact transport approval:
// the applied Marshal-reviewed MCP decision, or the runtime approved-plan
// proof. current also requires a Marshal review's launch to be live.
func (r *Runtime) transportReviewProof(ctx context.Context, approvalID string, current bool) (store.MCPTransportReview, error) {
	proof, err := r.store.AppliedMCPTransportReview(ctx, r.ProjectID(), approvalID)
	if err == nil {
		if current && !r.transportSourceCurrent(proof.Source.Launch, proof.Source.Owner, proof.Source.Provider) {
			return proof, fmt.Errorf("transport review launch revoked or superseded")
		}
		return proof, nil
	}
	if runtimeProof, runtimeErr := r.approvedPlanTransportProof(ctx, approvalID); runtimeErr == nil {
		return runtimeProof, nil
	}
	return store.MCPTransportReview{}, err
}

func isApprovedPlanTransportProof(proof store.MCPTransportReview) bool {
	return proof.Source.Launch == execution.ApprovedPlanDecider && proof.Source.Owner == approvedPlanProofOwner && proof.Source.Provider == approvedPlanProofProvider
}
