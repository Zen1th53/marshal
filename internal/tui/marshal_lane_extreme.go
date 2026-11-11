//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/permission"
	"strings"
	"unicode"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func privateMarshalProposalKeys(p marshalProposal) ([]string, bool) {
	if p.action == "goal" {
		if _, err := marshalGoalCriteria(p.criteria); err != nil || strings.TrimSpace(p.value) == "" || len(p.value) > 2000 {
			return nil, false
		}
		if p.criteria == "" {
			return []string{"action", "value"}, true
		}
		return []string{"action", "value", "criteria"}, true
	}
	if p.action != "approve-task" || p.id == "" || len(p.id) > 200 || strings.ContainsFunc(p.id, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.'
	}) {
		return nil, false
	}
	return []string{"action", "id"}, true
}

func privateProcess05ApprovalBound(ctx context.Context, service *app.ExecutionService, run marshal.Run, child execution.ExecutionRun, projectID, approvalID string) bool {
	if !run.Process05Bound || !run.Process05Parallel || child.ParentRunID == "" || child.PlanID != run.PlanID || child.PlanVersion != run.PlanVersion || string(child.ProjectID) != projectID || child.Delivery != execution.DeliveryPreserveBranch || child.State != execution.RunNeedsApproval {
		return false
	}
	parent, err := service.GetRun(ctx, child.ParentRunID)
	if err != nil || parent.ParentRunID != "" || parent.PlanID != run.PlanID || parent.PlanVersion != run.PlanVersion || string(parent.ProjectID) != projectID || parent.BaseCommit != run.BaseCommit || parent.TaskLanes[child.LaneTaskID] != child.RunID {
		return false
	}
	if err := service.Engine().ValidateTaskLaneBinding(ctx, child.RunID); err != nil {
		return false
	}
	for _, task := range run.Tasks {
		if task.PlanTaskID != child.LaneTaskID {
			continue
		}
		actual, ok := child.Tasks[task.PlanTaskID]
		return ok && task.Mode == marshal.Governed && task.Worker == actual.AssignedHarness && task.BaseCommit == child.BaseCommit && actual.State == execution.TaskNeedsApproval && actual.ApprovalID == approvalID
	}
	return false
}

func (w *Workspace) pendingProcess05Approval(ctx context.Context, run marshal.Run, approvalID string) bool {
	if w.runtime == nil {
		return false
	}
	service := w.runtime.Execution()
	// The approval manager holds the canonical decision state; run projections
	// only advertise a question. A parent's retained pre-decision snapshot must
	// never re-present a transport question for a resolved or expired approval.
	pending, err := service.Engine().ApprovalManager().GetApproval(approvalID)
	if err != nil || pending.Status != execution.ApprovalRequested {
		return false
	}
	runs, err := service.ListRuns(ctx)
	if err != nil {
		return false
	}
	for _, actual := range runs {
		if marshalProcess05ApprovalBound(run, actual, w.runtime.ProjectIdentity(), approvalID) || privateProcess05ApprovalBound(ctx, service, run, actual, w.runtime.ProjectIdentity(), approvalID) {
			return true
		}
	}
	return false
}

func (w *Workspace) describePrivateTransportApproval(req *permission.Request, id string) {
	if w.runtime == nil {
		return
	}
	a, err := w.runtime.Execution().Engine().ApprovalManager().GetApproval(id)
	if err != nil || a.OperationType != "PROVIDER_TRANSPORT" {
		return
	}
	req.Reason = "Separate explicit review of high-risk provider API transport only to " + a.TargetResource + "; task execution approval does not authorize this transport; credentials remain separately governed"
	req.Scope += "; " + a.Scope
	body, _ := json.Marshal(a)
	req.ApprovalBinding = string(body)
}
