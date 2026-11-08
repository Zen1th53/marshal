//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
)

type process05ResumeLane struct {
	Task, CanonicalTask, Run, Provider, State, TaskState, Approval, ActionDigest, StateDigest, NativeLaunch, NativeReceipt string
	Revision, ApprovalRevision                                                                                             int64
}
type process05ResumeSnapshot struct {
	Run, Plan, State, Base, Parent, ParentState, Goal   string
	Revision, PlanVersion, ParentRevision, GoalRevision int64
	Budget                                              marshal.Budget
	Lanes                                               []process05ResumeLane
	Tasks                                               []struct{ Task, State, Result, Frontier string }
}

// A proof exists only inside decidePermission after beginProposalMCPApply has
// validated and acquired the authority mutex. It expires before that guard
// finishes/unlocks, so retained contexts cannot reuse the held-lock path.
type privateHeldProposalLeaseKey struct{}
type privateHeldProposalLease struct {
	workspace *Workspace
	lease     *proposalMCPLease
	mu        sync.Mutex
	active    bool
}

// Caller has successfully entered beginProposalMCPApply. SourceLaunch alone is
// not a credential: the guard has already checked the protected receipt and
// holds the mutex. Unbound operator applications get no held-lock proof.
func (w *Workspace) privateProposalMCPApplyContext(ctx context.Context, req permission.Request) (context.Context, func()) {
	if req.SourceLaunch == "" {
		return ctx, func() {}
	}
	p := &privateHeldProposalLease{workspace: w, lease: w.proposalMCP.active}
	p.active = p.lease != nil && p.lease.id == req.SourceLaunch && w.validateProposalLease(p.lease) == nil
	return context.WithValue(ctx, privateHeldProposalLeaseKey{}, p), func() { p.mu.Lock(); p.active = false; p.mu.Unlock() }
}

func (w *Workspace) privateProcess05ResumeLease(ctx context.Context) (launch, owner, provider string, live bool) {
	if p, present := ctx.Value(privateHeldProposalLeaseKey{}).(*privateHeldProposalLease); present {
		// The typed proof cannot substitute a stale lease or a different workspace.
		// The original application guard still owns the authority mutex here.
		if p == nil {
			return "", "", "", false
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		// Expiration waits for this complete borrow before the outer guard can
		// unlock. Escaped/retained contexts therefore cannot race revocation.
		if p.workspace != w || !p.active || w.validateProposalLease(p.lease) != nil {
			return "", "", "", false
		}
		return p.lease.id, p.lease.owner, p.lease.provider, true
	}
	w.proposalMCP.mu.Lock()
	defer w.proposalMCP.mu.Unlock()
	lease := w.proposalMCP.active
	if w.validateProposalLease(lease) != nil {
		return "", "", "", false
	}
	return lease.id, lease.owner, lease.provider, true
}

// Read current durable evidence. This is a readiness observation, never a
// decision or a claim of completed work, model identity, or paid inference.
func (w *Workspace) privateProcess05ResumeSnapshot(ctx context.Context) (string, bool) {
	if w.runtime == nil || w.store == nil {
		return "", false
	}
	m := w.marshalSession()
	m.mu.Lock()
	service, runID, pending := m.service, m.runID, m.pending
	m.mu.Unlock()
	if service == nil || runID == "" || pending != nil {
		return "", false
	}
	record, err := service.Store.GetMarshalRun(ctx, service.ProjectID, runID)
	if err != nil || !record.Value.Process05Bound || !record.Value.Process05Parallel || record.Value.State != marshal.AwaitingUser {
		return "", false
	}
	run := record.Value
	if run.Budget != run.Settings.Budget {
		return "", false
	}
	snapshot := process05ResumeSnapshot{Run: runID, Plan: run.PlanID, PlanVersion: run.PlanVersion, Revision: record.Revision, State: string(run.State), Base: run.BaseCommit, Budget: run.Budget}
	e := w.runtime.Execution().Engine()
	if e == nil || e.ApprovalManager() == nil {
		return "", false
	}
	launch, owner, provider, live := w.privateProcess05ResumeLease(ctx)
	if !live {
		return "", false
	}
	seen := map[string]bool{}
	var blockedFuture []marshal.Task
	for _, task := range run.Tasks {
		frontier := "observed"
		if task.State == marshal.Queued {
			frontier = "awaiting_gate"
		}
		if task.State == marshal.Queued && task.Process05WaitApprovalID == "" {
			blocked := false
			for _, dep := range task.DependsOn {
				found := false
				for _, other := range run.Tasks {
					if other.PlanTaskID == dep {
						found = true
						blocked = blocked || other.State != marshal.Merged
					}
				}
				if !found {
					return "", false
				}
			}
			if !blocked || task.Process05WaitRunID != "" {
				return "", false
			}
			frontier = "dependency_blocked"
			blockedFuture = append(blockedFuture, task)
		}
		snapshot.Tasks = append(snapshot.Tasks, struct{ Task, State, Result, Frontier string }{task.PlanTaskID, string(task.State), task.ResultCommit, frontier})
		if task.State != marshal.Queued || frontier == "dependency_blocked" {
			continue
		}
		if task.Mode != marshal.Governed || task.Process05WaitRunID == "" || seen[task.Process05WaitApprovalID] {
			return "", false
		}
		seen[task.Process05WaitApprovalID] = true
		child, err := e.GetRun(ctx, task.Process05WaitRunID)
		if err != nil || e.ValidateTaskLaneBinding(ctx, child.RunID) != nil || e.ValidateRunGoal(ctx, child) != nil || child.State != execution.RunPaused || child.PlanID != run.PlanID || child.PlanVersion != run.PlanVersion || string(child.ProjectID) != w.runtime.ProjectIdentity() || child.BaseCommit != task.BaseCommit || child.LaneTaskID != task.PlanTaskID {
			return "", false
		}
		actual, ok := child.Tasks[task.PlanTaskID]
		if !ok || actual.State != execution.TaskReady || actual.AssignedHarness != task.Worker || actual.ApprovalID != task.Process05WaitApprovalID || actual.NativeTurn != nil {
			return "", false
		}
		parent, err := e.GetRun(ctx, child.ParentRunID)
		if err != nil || parent.TaskLanes[task.PlanTaskID] != child.RunID || parent.PlanID != run.PlanID || parent.PlanVersion != run.PlanVersion || parent.BaseCommit != run.BaseCommit {
			return "", false
		}
		if snapshot.Parent == "" {
			snapshot.Parent = parent.RunID
			snapshot.ParentRevision = parent.Version
			snapshot.ParentState = string(parent.State)
			snapshot.Goal = parent.GoalID
			snapshot.GoalRevision = parent.GoalRevision
		} else if snapshot.Parent != parent.RunID || snapshot.ParentRevision != parent.Version {
			return "", false
		}
		approval, err := e.ApprovalManager().GetApproval(actual.ApprovalID)
		if err != nil || approval.Status != execution.ApprovalApproved || approval.OperationType != "PROVIDER_TRANSPORT" || approval.RunID != child.RunID || approval.TaskID != task.PlanTaskID || approval.PlanID != run.PlanID || approval.PlanVersion != run.PlanVersion || approval.ExpiresAt == nil || !time.Now().Before(*approval.ExpiresAt) {
			return "", false
		}
		proof, err := w.store.AppliedMCPTransportReview(ctx, w.projectID, approval.ApprovalID)
		if err != nil || proof.Source.Launch != launch || proof.Source.Owner != owner || proof.Source.Provider != provider || proof.Request.RunID != runID || proof.Request.TaskID != marshalRunProposalBinding(run.PlanVersion, record.Revision, nil) {
			return "", false
		}
		var reviewed execution.RuntimeApproval
		if json.Unmarshal([]byte(proof.Request.ApprovalBinding), &reviewed) != nil || reviewed.ApprovalID != approval.ApprovalID || reviewed.RunID != child.RunID || reviewed.TaskID != task.PlanTaskID || reviewed.PlanID != run.PlanID || reviewed.PlanVersion != run.PlanVersion || reviewed.OperationType != approval.OperationType || reviewed.TargetResource != approval.TargetResource || reviewed.ActionDigest != approval.ActionDigest || reviewed.StateDigest != approval.StateDigest || reviewed.Status != execution.ApprovalRequested {
			return "", false
		}
		normalized := *approval
		normalized.Revision = reviewed.Revision
		normalized.Status = reviewed.Status
		normalized.ApprovedBy = reviewed.ApprovedBy
		normalized.DecisionReason = reviewed.DecisionReason
		normalized.DecisionCommandKey = reviewed.DecisionCommandKey
		normalized.ResolvedAt = reviewed.ResolvedAt
		frozen, err := json.Marshal(normalized)
		if err != nil || string(frozen) != proof.Request.ApprovalBinding {
			return "", false
		}
		snapshot.Lanes = append(snapshot.Lanes, process05ResumeLane{Task: task.PlanTaskID, CanonicalTask: actual.CanonicalTaskID, Run: child.RunID, Provider: actual.AssignedHarness, State: string(child.State), TaskState: string(actual.State), Revision: child.Version, Approval: approval.ApprovalID, ApprovalRevision: approval.Revision, ActionDigest: approval.ActionDigest, StateDigest: approval.StateDigest, NativeLaunch: proof.Source.Launch, NativeReceipt: proof.Source.ID})
	}
	if len(snapshot.Lanes) == 0 {
		return "", false
	}
	if len(blockedFuture) > 0 {
		parent, err := e.GetRun(ctx, snapshot.Parent)
		if err != nil {
			return "", false
		}
		for _, future := range blockedFuture {
			original, ok := parent.Tasks[future.PlanTaskID]
			if !ok || original.State != execution.TaskPending || parent.TaskLanes[future.PlanTaskID] != "" || !slices.Equal(original.Dependencies, future.DependsOn) {
				return "", false
			}
		}
	}
	// An unanswered/denied current-run question cannot be hidden by approved
	// siblings or by an expected lane count. Late arrivals retain their gates.
	w.permissions.mu.Lock()
	for _, p := range w.permissions.build.pending {
		if p.request.RunID == runID && strings.HasPrefix(p.request.Object, "/marshal approve-task ") {
			w.permissions.mu.Unlock()
			return "", false
		}
	}
	w.permissions.mu.Unlock()
	data, err := json.Marshal(snapshot)
	if err != nil || len(data) > privateDecisionPacketBytes/2 {
		return "", false
	}
	return string(data), true
}

func (w *Workspace) describePrivateProcess05Resume(req *permission.Request, p marshalProposal) bool {
	if p.action != "resume" || p.who != "MARSHAL runtime" {
		return true
	}
	if snapshot, ok := w.privateProcess05ResumeSnapshot(context.Background()); ok {
		req.ApprovalBinding = snapshot
		req.Scope += "; current bound continuation snapshot " + snapshot
		req.Reason = "Review resuming the already approved bound lanes after their applied transport decisions. Task states and result commits below are current observations; pending/empty results are pre-execution, not verified completion. Future task/tool gates, acceptance and delivery remain separate. Decide this resume independently."
		return true
	}
	return false
}
func (w *Workspace) validatePrivateProcess05Resume(ctx context.Context, req permission.Request, p marshalProposal) error {
	if p.action != "resume" || p.who != "MARSHAL runtime" {
		return nil
	}
	snapshot, ok := w.privateProcess05ResumeSnapshot(ctx)
	if !ok || req.ApprovalBinding == "" || snapshot != req.ApprovalBinding {
		return errors.New("bound Process 05 continuation readiness changed; review current gates again")
	}
	return nil
}
