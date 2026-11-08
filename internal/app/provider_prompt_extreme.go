//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
)

// This private observation is minted by reviewed runtime admission and marked
// only after both one-shot receipts and their durable audit succeeded. It does
// not replace an approval, change the immutable package, or grant another turn.
type providerPromptAdmission struct {
	mu                           sync.Mutex
	runtime                      *Runtime
	binding                      providerTransportBinding
	consumed, used               bool
	egressID, launch, occurrence string
}

func (r *Runtime) providerConstraintPrompt(ctx context.Context, admitted execution.TaskExecution, pkg execution.ConstraintPackage, worktree string) (string, error) {
	header := pkg.FormatPromptHeader()
	auth, ok := ctx.Value(providerTransportContextKey{}).(providerTransportAuthorization)
	if !ok {
		return header, nil // default and injected adapters retain existing prose
	}
	if !validProviderTransportEndpoints(auth.binding) {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	if err := execution.VerifyConstraintPackage(pkg); err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	marker := auth.prompt
	if marker == nil {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	marker.mu.Lock()
	sealed := marker.runtime == r && reflect.DeepEqual(marker.binding, auth.binding) && marker.consumed && !marker.used && marker.egressID != "" && marker.launch != "" && marker.occurrence != ""
	launch, occurrence := marker.launch, marker.occurrence
	marker.mu.Unlock()
	if !sealed {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	b := cloneProviderTransportBinding(auth.binding)
	if b.Project != r.ProjectIdentity() || b.Run != admitted.RunID || b.Task != admitted.TaskID || b.CanonicalTask != admitted.CanonicalTaskID || b.Worktree != filepath.Clean(worktree) || admitted.WorktreePath != b.Worktree || transportPackageDigest(pkg) != b.Package {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	run, err := r.Execution().Engine().GetRun(ctx, b.Run)
	if err != nil {
		return "", err
	}
	task, found := run.Tasks[b.Task]
	if !found || run.State != execution.RunRunning || task.State != execution.TaskRunning || task.NativeTurn != nil || task.Attempts != b.Attempt || task.RunRevision != admitted.RunRevision || task.LeaseID != admitted.LeaseID || task.Mutates != admitted.Mutates || task.Attempts != admitted.Attempts || task.BaseCommit != admitted.BaseCommit || task.Description != admitted.Description || !reflect.DeepEqual(task.TargetFiles, admitted.TargetFiles) || task.ApprovalID != auth.approvalID || task.CanonicalTaskID != b.CanonicalTask || task.WorktreePath != b.Worktree || task.BaseCommit != b.Base || task.AssignedHarness != b.Provider || task.AssignedModel != b.Model || task.AssignedRole != b.AgentRole || task.AssignedAgent != b.AssignedAgent || run.PlanID != b.Plan || run.PlanVersion != b.PlanVersion || string(run.ProjectID) != b.Project {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	if err := r.Execution().Engine().ValidateWorkerAttempt(ctx, run, task); err != nil {
		return "", err
	}
	if err := r.Execution().Engine().ValidateRunGoal(ctx, run); err != nil {
		return "", err
	}
	if run.ParentRunID != "" {
		if err := r.Execution().Engine().ValidateTaskLaneBinding(ctx, run.RunID); err != nil {
			return "", err
		}
	}
	planDigest, err := r.transportPlanDigest(ctx, run, task)
	if err != nil || planDigest != b.PlanDigest {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	goal, err := r.store.GetGoalContract(ctx, run.GoalID, run.GoalRevision)
	if err != nil {
		return "", err
	}
	plan, err := r.store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return "", err
	}
	current, err := execution.BuildConstraintPackage(goal, task, plan)
	if err != nil || transportPackageDigest(current) != b.Package {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	proof, err := r.transportReviewProof(ctx, auth.approvalID, true)
	if err != nil || proof.Source.Launch != launch || proof.Source.ID != occurrence {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	head, err := gitMarshal(ctx, b.Worktree, "rev-parse", "HEAD")
	if err != nil || head != b.Head {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	tree, err := execution.WorkspaceTreeDigest(b.Worktree)
	if err != nil || tree != b.Tree {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	req := transportApprovalRequest(b)
	transport, err := r.Execution().Engine().ApprovalManager().GetApproval(auth.approvalID)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if transport.Status != execution.ApprovalConsumed || transport.OperationType != providerTransportOperation || transport.RunID != b.Run || transport.TaskID != b.Task || transport.PlanID != b.Plan || transport.PlanVersion != b.PlanVersion || transport.Scope != req.Scope || transport.TargetResource != req.TargetResource || transport.ActionDigest != auth.action || transport.StateDigest != auth.state || transport.ExpiresAt == nil || !now.Before(*transport.ExpiresAt) {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	if err := r.validateTransportTaskApprovalStatus(b, run, task, now, execution.ApprovalConsumed); err != nil {
		return "", err
	}
	required, err := r.transportTaskApprovalRequired(ctx, run, task)
	if err != nil || (required && !b.Original.Required) {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	// Runtime authorization is already resolved for this single admitted turn.
	// Retain all package constraints/digest and require new approval for changes.
	identity := map[string]any{"transport_approval": auth.approvalID, "run": b.Run, "task": b.Task, "plan": b.Plan, "plan_version": b.PlanVersion, "attempt": b.Attempt}
	authority := "The approved canonical plan does not require an ordinary TASK_EXECUTE approval for this task; its exact Marshal-reviewed brokered transport approval was consumed."
	if b.Original.Required {
		identity["task_approval"] = b.Original.ID
		authority = "Both exact task and separate Marshal-reviewed brokered transport approvals were consumed by the runtime."
		if isApprovedPlanTransportProof(proof) {
			authority = "Both exact task and separate brokered transport approvals were granted by the runtime under the owner's approved-plan rule and consumed by the runtime."
		}
	}
	resolved, _ := json.Marshal(identity)
	statement := fmt.Sprintf("MARSHAL AUTHORIZATION RESOLVED FOR THIS ADMITTED ATTEMPT: %s. %s Execute only the approved task and constraints; do not request duplicate human approval for that same scope. Scope changes, new attempts, network expansion and final acceptance require their own authorization.\n", resolved, authority)
	rendered, err := resolvedProviderPromptHeader(header, statement)
	if err != nil {
		return "", err
	}
	marker.mu.Lock()
	if marker.used {
		marker.mu.Unlock()
		return "", execution.ErrApprovalTOCTOUViolation
	}
	marker.used = true
	marker.mu.Unlock()
	return rendered, nil
}

// Raw canonical descriptions may themselves contain the warning text. Do not
// change those fields or leave an ambiguous contradictory envelope.
func resolvedProviderPromptHeader(header, statement string) (string, error) {
	const warning = "WARNING: Hard human approval required before mutating changes.\n"
	if strings.Count(header, warning) != 1 {
		return "", execution.ErrApprovalTOCTOUViolation
	}
	return strings.Replace(header, warning, statement, 1), nil
}
