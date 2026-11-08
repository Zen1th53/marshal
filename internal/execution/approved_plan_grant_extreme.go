//go:build !governed

package execution

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

// Owner rule (Extreme only): task execution inside the owner/Marshal-approved
// plan scope does not wait for per-task approvals. The runtime grants exactly
// those approvals itself, under a reserved decider no command can name, and
// records evidence. Anything outside the approved scope still waits.
const (
	ApprovedPlanDecider = "runtime:extreme-approved-plan"
	ApprovedPlanReason  = "owner rule (Marshal): execution inside the owner/Marshal-approved plan scope does not wait for per-task approval; granted by the runtime with recorded evidence"
	ApprovedPlanEvent   = "APPROVAL_GRANTED_BY_APPROVED_PLAN"
)

// ApprovedPlanExecution binds one dispatch to the exact approved canonical
// plan of a Process 05 bound Marshal run. Only the governed Marshal runner
// constructs it, after its own plan, goal and handoff checks; the engine
// re-checks the plan itself and never treats the binding as a grant.
type ApprovedPlanExecution struct {
	MarshalRun, Run, Task, Plan string
	PlanVersion                 int64
	// Transport checks the exact brokered transport binding and durably
	// records the runtime proof before the engine decides the approval.
	Transport func(ctx context.Context, run ExecutionRun, task TaskExecution, original, transport RuntimeApproval) error
}

type approvedPlanExecutionKey struct{}
type approvedPlanDecisionKey struct{}

func WithApprovedPlanExecution(ctx context.Context, b ApprovedPlanExecution) context.Context {
	return context.WithValue(ctx, approvedPlanExecutionKey{}, b)
}

// IsApprovedPlanGrant reports whether the runtime itself decided an approval
// under the owner rule. Operator and Marshal decisions never match: the
// decider is reserved and refused on every other decision path.
func IsApprovedPlanGrant(a RuntimeApproval) bool {
	return (a.Status == ApprovalApproved || a.Status == ApprovalConsumed) && a.ApprovedBy == ApprovedPlanDecider && a.DecisionReason == ApprovedPlanReason && a.DecisionCommandKey == "" && !a.HardViolation && a.ResolvedAt != nil
}

func reservedApprovalDecider(ctx context.Context, decider string) bool {
	private, _ := ctx.Value(approvedPlanDecisionKey{}).(bool)
	return decider == ApprovedPlanDecider && !private
}

// Approval kinds a plan approval already covers. Privilege and out-of-scope
// gates are predicted from the whole goal's assessment and attached to every
// task; the worker runs confined in its sandbox, so inside the approved plan
// they add nothing a task-level decision could check. Destructive,
// irreversible and sensitive-data gates are covered only when soft. Secrets,
// network, production deploys and external effects keep their wait, as does
// any hard destructive, irreversible or sensitive gate.
func approvedPlanApprovalKind(a plan.ApprovalRequirement) bool {
	switch a.Kind {
	case plan.ApprovalOutOfScope, plan.ApprovalPrivilege:
		return true
	case plan.ApprovalDestructive, plan.ApprovalIrreversible, plan.ApprovalSensitiveData:
		return !a.Hard
	}
	return false
}

// approvedPlanScope reports the approved-plan binding when this exact task of
// this exact run is still inside the current approved canonical plan scope.
func (e *Engine) approvedPlanScope(ctx context.Context, run ExecutionRun, task TaskExecution) (ApprovedPlanExecution, bool) {
	b, ok := ctx.Value(approvedPlanExecutionKey{}).(ApprovedPlanExecution)
	if !ok || b.MarshalRun == "" || b.Run != run.RunID || b.Task != task.TaskID || b.Plan != run.PlanID || b.PlanVersion != run.PlanVersion || run.PlanID == "" || run.PlanVersion < 1 {
		return b, false
	}
	if run.State != RunRunning || run.SelectedTask != task.TaskID || task.RunID != run.RunID || task.NativeTurn != nil || !task.ApprovalRequired || e.planReader == nil || e.goalReader == nil {
		return b, false
	}
	if e.ValidateRunGoal(ctx, run) != nil {
		return b, false
	}
	if run.ParentRunID != "" && e.ValidateTaskLaneBinding(ctx, run.RunID) != nil {
		return b, false
	}
	p, err := e.planReader.GetActivePlan(ctx, run.ProjectID)
	if err != nil || p.ID != run.PlanID || p.Version != run.PlanVersion || p.ProjectID != run.ProjectID || p.State != plan.StateApproved || p.Goal.GoalID != run.GoalID || p.Goal.Revision != run.GoalRevision || p.ApprovedScope == nil || p.ApprovalScopeDigest != p.ApprovedScope.Digest() || p.ApprovedScope.Goal != p.Goal {
		return b, false
	}
	var original, planned *plan.Task
	for i := range p.ApprovedScope.Tasks {
		if p.ApprovedScope.Tasks[i].ID == task.TaskID {
			if original != nil {
				return b, false
			}
			original = &p.ApprovedScope.Tasks[i]
		}
	}
	for i := range p.Tasks {
		if p.Tasks[i].ID == task.TaskID {
			if planned != nil {
				return b, false
			}
			planned = &p.Tasks[i]
		}
	}
	if original == nil || planned == nil || !reflect.DeepEqual(*original, *planned) || !slices.Equal(planned.Paths, task.TargetFiles) || planned.Title != task.Description || planned.Mutating != task.Mutates || planned.NeedsNetwork || !slices.Equal(p.Checks[task.TaskID], p.ApprovedScope.Checks[task.TaskID]) {
		return b, false
	}
	// The worker must be the plan's own producer for this task, or the
	// provider its lane parent durably records for it after an operator
	// reassign or provider switch (owner rule: in Extreme a reassigned task
	// inside the approved plan does not wait for a task approval either).
	assigned := false
	for _, a := range p.Assignments.Assignments {
		if !a.Independent && executionHarnessName(a.Harness) == task.AssignedHarness && a.Model == task.AssignedModel && slices.Contains(a.Tasks, task.TaskID) {
			assigned = true
		}
	}
	if !assigned && run.ParentRunID != "" {
		if parent, err := e.store.GetRun(ctx, run.ParentRunID); err == nil && ProviderSwitchAssigns(parent, task) {
			assigned = true
		}
	}
	required := planned.RequiresApproval
	for _, a := range p.Approvals {
		if a.Task != task.TaskID {
			continue
		}
		if !approvedPlanApprovalKind(a) {
			return b, false
		}
		required = true
	}
	return b, assigned && required
}

// grantApprovedPlanTask decides the engine's own TASK_EXECUTE request when it
// is exactly the approved plan's task scope. Any mismatch leaves it pending.
func (e *Engine) grantApprovedPlanTask(ctx context.Context, run ExecutionRun, task TaskExecution, app RuntimeApproval) bool {
	b, ok := e.approvedPlanScope(ctx, run, task)
	if !ok {
		return false
	}
	target := filepath.Join(task.TargetFiles...)
	now := time.Now().UTC()
	if app.OperationType != "TASK_EXECUTE" || app.Scope != "task_mutation" || app.RiskLevel != model.R2 || app.Status != ApprovalRequested || app.HardViolation || app.RunID != run.RunID || app.TaskID != task.TaskID || app.PlanID != run.PlanID || app.PlanVersion != run.PlanVersion || app.TargetResource != target || app.ActionDigest != ComputeActionDigest("TASK_EXECUTE", target, "", task.Description) || app.StateDigest != ComputeStateDigest("") || app.ExpiresAt == nil || !now.Before(*app.ExpiresAt) || task.ApprovalID != app.ApprovalID {
		return false
	}
	if e.decideApprovedPlan(app.ApprovalID, now) != nil {
		return false
	}
	_, _ = e.journal.Append(JournalEvent{RunID: run.RunID, TaskID: task.TaskID, Actor: ApprovedPlanDecider, EventType: ApprovedPlanEvent, Summary: fmt.Sprintf("TASK_EXECUTE approval %s granted by the approved plan (marshal run %s, plan %s v%d, target %q, action %s): %s", app.ApprovalID, b.MarshalRun, run.PlanID, run.PlanVersion, target, app.ActionDigest, ApprovedPlanReason)})
	return true
}

// grantApprovedPlanTransport decides the brokered PROVIDER_TRANSPORT request
// of a task whose TASK_EXECUTE approval the approved plan itself granted. The
// runtime proof is recorded first; a failed record or decision leaves the
// request pending for the ordinary Marshal decision.
func (e *Engine) grantApprovedPlanTransport(ctx context.Context, run ExecutionRun, task TaskExecution, originalID string, app RuntimeApproval) bool {
	b, ok := e.approvedPlanScope(ctx, run, task)
	if !ok || b.Transport == nil || originalID == "" || task.ApprovalID != app.ApprovalID {
		return false
	}
	now := time.Now().UTC()
	original, err := e.approvals.GetApproval(originalID)
	if err != nil || !IsApprovedPlanGrant(*original) || original.Status != ApprovalApproved || original.OperationType != "TASK_EXECUTE" || original.RunID != run.RunID || original.TaskID != task.TaskID || original.PlanID != run.PlanID || original.PlanVersion != run.PlanVersion || original.ExpiresAt == nil || !now.Before(*original.ExpiresAt) {
		return false
	}
	if app.OperationType != "PROVIDER_TRANSPORT" || app.RiskLevel != model.R3 || app.Status != ApprovalRequested || app.HardViolation || app.RunID != run.RunID || app.TaskID != task.TaskID || app.PlanID != run.PlanID || app.PlanVersion != run.PlanVersion || app.ExpiresAt == nil || !now.Before(*app.ExpiresAt) {
		return false
	}
	if b.Transport(ctx, run, task, *original, app) != nil {
		return false
	}
	if e.decideApprovedPlan(app.ApprovalID, now) != nil {
		return false
	}
	_, _ = e.journal.Append(JournalEvent{RunID: run.RunID, TaskID: task.TaskID, Actor: ApprovedPlanDecider, EventType: ApprovedPlanEvent, Summary: fmt.Sprintf("PROVIDER_TRANSPORT approval %s granted by the approved plan (marshal run %s, plan %s v%d, task approval %s, target %q, action %s): %s", app.ApprovalID, b.MarshalRun, run.PlanID, run.PlanVersion, originalID, app.TargetResource, app.ActionDigest, ApprovedPlanReason)})
	return true
}

// A fresh context: a caller's decision-command binding must not attach to a
// runtime decision, and only this path may name the reserved decider.
func (e *Engine) decideApprovedPlan(id string, now time.Time) error {
	ctx := context.WithValue(context.Background(), approvedPlanDecisionKey{}, true)
	_, err := e.approvals.DecideContext(ctx, id, true, ApprovedPlanDecider, ApprovedPlanReason, now)
	return err
}
