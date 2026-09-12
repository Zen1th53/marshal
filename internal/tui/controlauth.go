package tui

// The adapter from the workspace's canonical handles to the Control authority.
//
// Every method forwards to canonical code and nothing else. Where a canonical
// path does not exist the method returns an explicit error rather than
// improvising one, so an unbound capability surfaces as a refusal in the UI
// instead of as a silent local write.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/optimization"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/resources"
	"github.com/Zen1th53/marshal/internal/startup"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

// runtimeControlAuthority binds Control to the canonical runtime.
type runtimeControlAuthority struct {
	runtime *app.Runtime
	store   *store.Store
	// gate is the canonical ULTRA authority. It is read live rather than
	// captured, because the Cloud handshake completes after the workspace is
	// built.
	gate func() *cloud.Gate
	// requestULTRA asks the Cloud for an entitlement through the canonical
	// client. It is a function so the workspace supplies its own wiring.
	requestULTRA func(ctx context.Context) error
	sessionID    string
	// projectID is the canonical project identity the plan and execution
	// services are keyed by.
	projectID projectid.ID
	// currentRun remembers the run this session started, so subsequent
	// actions act on it. It is a correlation, not a cache of run state.
	mu         sync.Mutex
	currentRun string

	// The workspace owns the two mode preferences, per the frozen specs.
	// They are functions rather than a pointer so the adapter reaches the
	// live workspace state under its own lock.
	setMode       func(mode string) error
	mode          func() string
	setPreference func(enabled bool) error
	preference    func() bool
	requestExit   func() error
	exitRequested func() bool
	// replaceRuntime updates the workspace after a stopped-runtime restore.
	// It does not perform the restore; it only swaps in the canonical runtime
	// that app.Open produced after store.RestoreDatabase succeeded.
	replaceRuntime func(*app.Runtime)
}

// Providers adapts the canonical runtime discovery result to the read-only TUI
// contract. It carries no execution authority and never launches a harness.
func (a *runtimeControlAuthority) Providers(ctx context.Context) ([]ProviderProbe, error) {
	if a == nil || a.runtime == nil {
		return nil, errNoRuntime
	}
	rows, err := a.runtime.ProviderObservations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderProbe, 0, len(rows))
	for _, row := range rows {
		out = append(out, ProviderProbe{
			Name: row.Name, Found: row.Found, Path: row.Path,
			Probed: row.Probed, ProbeNote: row.Note,
		})
	}
	return out, nil
}

var errNoRuntime = errors.New("no runtime is attached to this workspace")

func (a *runtimeControlAuthority) execution() (*app.ExecutionService, error) {
	if a == nil || a.runtime == nil {
		return nil, errNoRuntime
	}
	service := a.runtime.Execution()
	if service == nil {
		return nil, errors.New("the execution service is unavailable in this runtime")
	}
	return service, nil
}

func (a *runtimeControlAuthority) StartRun(ctx context.Context, sessionID string, target Target) (execution.ExecutionRun, error) {
	service, err := a.execution()
	if err != nil {
		return execution.ExecutionRun{}, err
	}
	run, err := service.StartRunBound(ctx, sessionID, a.projectID,
		target.ID, target.Revision, target.Digest)
	if err != nil {
		return execution.ExecutionRun{}, err
	}
	if run == nil {
		return execution.ExecutionRun{}, errors.New("the execution service returned no run")
	}
	a.mu.Lock()
	a.currentRun = run.RunID
	a.mu.Unlock()
	return *run, nil
}

func (a *runtimeControlAuthority) ExecuteRun(ctx context.Context, runID string, expectedVersion int64) (execution.ExecutionRun, error) {
	service, err := a.execution()
	if err != nil {
		return execution.ExecutionRun{}, err
	}
	run, err := service.ExecuteRunBound(ctx, runID, expectedVersion)
	if err != nil {
		return execution.ExecutionRun{}, err
	}
	if run == nil {
		return execution.ExecutionRun{}, errors.New("the execution service returned no run")
	}
	return *run, nil
}

func (a *runtimeControlAuthority) GetRun(ctx context.Context, runID string) (execution.ExecutionRun, error) {
	service, err := a.execution()
	if err != nil {
		return execution.ExecutionRun{}, err
	}
	return service.GetRun(ctx, runID)
}

func (a *runtimeControlAuthority) CurrentRunID(ctx context.Context) (string, error) {
	a.mu.Lock()
	currentRun := a.currentRun
	a.mu.Unlock()
	if currentRun != "" {
		return currentRun, nil
	}
	service, err := a.execution()
	if err != nil {
		return "", err
	}
	engine := service.Engine()
	if engine == nil {
		return "", errors.New("the execution engine is unavailable")
	}
	runs, err := engine.ListRuns(ctx)
	if err != nil {
		return "", err
	}
	// The newest run belonging to this session. Choosing by session rather
	// than by recency alone keeps one workspace from acting on another's run.
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].SessionID == a.sessionID {
			return runs[i].RunID, nil
		}
	}
	return "", nil
}

func (a *runtimeControlAuthority) ClaimTask(ctx context.Context, taskID, agentID string, expectedRevision int64) error {
	if a.runtime == nil {
		return errNoRuntime
	}
	_, err := a.runtime.Claim(ctx, app.ClaimRequest{
		TaskID:           taskID,
		AgentID:          agentID,
		ExpectedRevision: expectedRevision,
	})
	return err
}

func (a *runtimeControlAuthority) ReleaseTask(ctx context.Context, taskID string, expectedRevision int64, reason string) error {
	if a.runtime == nil {
		return errNoRuntime
	}
	// The runtime resolves the active lease and its expected revision itself.
	// Resolving them here as well would be a second lookup that could disagree
	// with the one the store actually checks.
	return a.runtime.Release(ctx, app.ReleaseRequest{
		TaskID:           taskID,
		BlockedReason:    reason,
		ExpectedRevision: expectedRevision,
		EnforceRevision:  true,
	})
}

func (a *runtimeControlAuthority) CancelTask(ctx context.Context, taskID string, expectedRevision int64) error {
	if a.runtime == nil {
		return errNoRuntime
	}
	return a.runtime.CancelTaskExpected(ctx, taskID, expectedRevision)
}

func (a *runtimeControlAuthority) Task(ctx context.Context, taskID string) (model.Task, error) {
	if a.runtime == nil {
		return model.Task{}, errNoRuntime
	}
	return a.runtime.Task(ctx, taskID)
}

func (a *runtimeControlAuthority) approvals() (*execution.ApprovalManager, error) {
	service, err := a.execution()
	if err != nil {
		return nil, err
	}
	engine := service.Engine()
	if engine == nil {
		return nil, errors.New("the execution engine is unavailable")
	}
	manager := engine.ApprovalManager()
	if manager == nil {
		return nil, errors.New("the approval manager is unavailable")
	}
	return manager, nil
}

// PendingApprovals lists approvals awaiting a decision.
//
// The approval manager exposes no listing, so the queue is read from the runs
// themselves: an ExecutionRun carries the approvals raised during it. Reading
// the run rather than the manager's private map also means the queue reflects
// durable state rather than one process's memory.
func (a *runtimeControlAuthority) PendingApprovals(ctx context.Context) ([]*execution.RuntimeApproval, error) {
	return a.approvalRecords(ctx, true)
}

// ResolvedApprovals returns the same session-scoped records after a decision,
// including canonical Goal and Plan lifecycle decisions.
func (a *runtimeControlAuthority) ResolvedApprovals(ctx context.Context) ([]*execution.RuntimeApproval, error) {
	return a.approvalRecords(ctx, false)
}

func (a *runtimeControlAuthority) approvalRecords(ctx context.Context, pendingOnly bool) ([]*execution.RuntimeApproval, error) {
	service, err := a.execution()
	if err != nil {
		return nil, err
	}
	engine := service.Engine()
	if engine == nil {
		return nil, errors.New("the execution engine is unavailable")
	}
	runs, err := engine.ListRuns(ctx)
	if err != nil {
		return nil, err
	}
	manager := engine.ApprovalManager()

	var records []*execution.RuntimeApproval
	if goal, goalErr := a.CurrentGoal(ctx); goalErr == nil && goal.Confirmation != model.ConfirmationNeedsInput {
		record := goalApprovalRecord(goal)
		if (record.Status == execution.ApprovalRequested) == pendingOnly {
			records = append(records, record)
		}
	}
	if plans, planErr := a.plans(); planErr == nil {
		if current, currentErr := plans.Current(ctx, a.projectID); currentErr == nil &&
			(current.State == plan.StateReady || current.State == plan.StateApproved || current.State == plan.StateCancelled) {
			record := planApprovalRecord(current)
			if (record.Status == execution.ApprovalRequested) == pendingOnly {
				records = append(records, record)
			}
		}
	}
	for _, run := range runs {
		if run.SessionID != a.sessionID {
			continue
		}
		ids := make([]string, 0, len(run.Approvals))
		for id := range run.Approvals {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			// Prefer the manager's copy, which is the authority on the current
			// status; fall back to the run's record when it has none.
			if manager != nil {
				if live, err := manager.GetApproval(id); err == nil && live != nil {
					if (live.Status == execution.ApprovalRequested) == pendingOnly {
						records = append(records, live)
					}
					continue
				}
			}
			record := run.Approvals[id]
			if (record.Status == execution.ApprovalRequested) == pendingOnly {
				copied := record
				records = append(records, &copied)
			}
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ApprovalID < records[j].ApprovalID
		}
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
	return records, nil
}

func (a *runtimeControlAuthority) Approval(ctx context.Context, approvalID string) (*execution.RuntimeApproval, error) {
	if strings.HasPrefix(approvalID, "goal-confirm:") {
		goal, err := a.CurrentGoal(ctx)
		if err != nil {
			return nil, err
		}
		record := goalApprovalRecord(goal)
		if record.ApprovalID != approvalID {
			return nil, fmt.Errorf("goal approval %s is stale", approvalID)
		}
		return record, nil
	}
	if strings.HasPrefix(approvalID, "plan-approve:") {
		service, err := a.plans()
		if err != nil {
			return nil, err
		}
		current, err := service.Current(ctx, a.projectID)
		if err != nil {
			return nil, err
		}
		record := planApprovalRecord(current)
		if record.ApprovalID != approvalID {
			return nil, fmt.Errorf("plan approval %s is stale", approvalID)
		}
		return record, nil
	}
	manager, err := a.approvals()
	if err != nil {
		return nil, err
	}
	return manager.GetApproval(approvalID)
}

// DecideApproval records a decision through the canonical execution service.
//
// The service delegates to the approval manager, which enforces expiry, the
// one-shot transition and the digest binding. Nothing here decides anything.
func (a *runtimeControlAuthority) DecideApproval(ctx context.Context, approvalID string, approve bool, approver, rationale string) error {
	if strings.HasPrefix(approvalID, "goal-confirm:") {
		current, err := a.Approval(ctx, approvalID)
		if err != nil {
			return err
		}
		if !approve {
			return errors.New("goal rejection is blocked: the canonical application-layer goal cancellation boundary is an IMPLEMENTATION GAP (CTUI-0048)")
		}
		_, err = a.runtime.ApproveGoal(ctx, a.sessionID, current.PlanVersion)
		return err
	}
	if strings.HasPrefix(approvalID, "plan-approve:") {
		current, err := a.Approval(ctx, approvalID)
		if err != nil {
			return err
		}
		service, err := a.plans()
		if err != nil {
			return err
		}
		if approve {
			_, err = service.ApproveVersion(ctx, a.projectID, current.PlanVersion)
		} else {
			_, err = service.CancelVersion(ctx, a.projectID, current.PlanVersion)
		}
		return err
	}
	service, err := a.execution()
	if err != nil {
		return err
	}
	if approve {
		return service.Approve(ctx, approvalID, approver, rationale)
	}
	return service.Reject(ctx, approvalID, approver, rationale)
}

func goalApprovalRecord(goal model.GoalContract) *execution.RuntimeApproval {
	raw, _ := json.Marshal(goal)
	actionDigest := execution.ComputeActionDigest("GOAL_CONFIRM", goal.ID, "", string(raw))
	stateDigest := execution.ComputeStateDigest(fmt.Sprintf("%s:%d", goal.Confirmation, goal.Revision))
	status := execution.ApprovalRequested
	if goal.Confirmation == model.ConfirmationApproved || goal.Confirmation == model.ConfirmationDelegated {
		status = execution.ApprovalApproved
	} else if goal.Confirmation == model.ConfirmationCancelled {
		status = execution.ApprovalDenied
	}
	return &execution.RuntimeApproval{
		ApprovalID: "goal-confirm:" + goal.ID,
		PlanID:     goal.ID, PlanVersion: goal.Revision, OperationType: "GOAL_CONFIRM",
		TargetResource: goal.ID, RiskLevel: goal.Risk, Scope: strings.Join(goal.Scope, ", "),
		ActionDigest: actionDigest, StateDigest: stateDigest, Status: status,
		CreatedAt: goal.UpdatedAt,
	}
}

func planApprovalRecord(current plan.ExecutionPlan) *execution.RuntimeApproval {
	state := string(current.State)
	status := execution.ApprovalRequested
	if current.State == plan.StateApproved {
		status = execution.ApprovalApproved
	} else if current.State == plan.StateCancelled {
		status = execution.ApprovalDenied
	}
	digest := fmt.Sprintf("%s/%s", current.Goal.RequestDigest, current.Goal.ConstraintDigest)
	return &execution.RuntimeApproval{
		ApprovalID: "plan-approve:" + current.ID,
		PlanID:     current.ID, PlanVersion: current.Version, OperationType: "PLAN_APPROVE",
		TargetResource: current.ID, Scope: string(current.ProjectID),
		ActionDigest: digest, StateDigest: execution.ComputeStateDigest(state), Status: status,
		CreatedAt: current.UpdatedAt,
	}
}

func (a *runtimeControlAuthority) plans() (*app.PlanService, error) {
	if a == nil || a.runtime == nil {
		return nil, errNoRuntime
	}
	service := a.runtime.Plans()
	if service == nil {
		return nil, errors.New("the plan service is unavailable in this runtime")
	}
	return service, nil
}

func (a *runtimeControlAuthority) ApprovePlan(ctx context.Context) (PlanState, error) {
	service, err := a.plans()
	if err != nil {
		return PlanState{}, err
	}
	approved, err := service.Approve(ctx, a.projectID)
	if err != nil {
		return PlanState{}, err
	}
	return planState(approved), nil
}

func (a *runtimeControlAuthority) CancelPlan(ctx context.Context, expectedVersion int64) (PlanState, error) {
	service, err := a.plans()
	if err != nil {
		return PlanState{}, err
	}
	cancelled, err := service.CancelVersion(ctx, a.projectID, expectedVersion)
	if err != nil {
		return PlanState{}, err
	}
	return planState(cancelled), nil
}

func (a *runtimeControlAuthority) CurrentPlan(ctx context.Context) (PlanState, error) {
	service, err := a.plans()
	if err != nil {
		return PlanState{}, err
	}
	current, err := service.Current(ctx, a.projectID)
	if err != nil {
		return PlanState{}, err
	}
	return planState(current), nil
}

// planState converts a canonical plan into the position Control binds to.
//
// Version is the plan's own CAS revision, which is what a revision-checked
// mutation must carry.
func planState(p plan.ExecutionPlan) PlanState {
	return PlanState{
		PlanID:  p.ID,
		Version: p.Version,
		Status:  string(p.State),
		// The goal binding is the plan's content identity. Both digests are
		// carried because either moving stales the plan: the request digest
		// covers the goal text, the constraint digest covers the hard
		// constraints, and a constraint added at the same revision would
		// otherwise be invisible here.
		Digest: fmt.Sprintf("%s/%s", p.Goal.RequestDigest, p.Goal.ConstraintDigest),
	}
}

func (a *runtimeControlAuthority) CurrentGoal(ctx context.Context) (model.GoalContract, error) {
	if a.store == nil {
		return model.GoalContract{}, errors.New("no store is attached")
	}
	return a.store.GetActiveGoalContract(ctx, a.sessionID)
}

func (a *runtimeControlAuthority) BudgetConsumed(ctx context.Context, goalID string, revision int64) (model.ConsumedBudget, error) {
	if a.store == nil {
		return model.ConsumedBudget{}, errors.New("no store is attached")
	}
	consumed, err := a.store.GetBudgetTracker(ctx, a.sessionID, goalID, revision)
	if err != nil {
		return model.ConsumedBudget{}, err
	}
	if consumed == nil {
		return model.ConsumedBudget{}, errors.New("no budget consumption is recorded yet")
	}
	return *consumed, nil
}

func (a *runtimeControlAuthority) GoalTermination(ctx context.Context, goalID string, revision int64) (model.GoalTermination, error) {
	if a.store == nil {
		return model.GoalTermination{}, errors.New("no store is attached")
	}
	term, err := a.store.GetGoalTermination(ctx, a.sessionID, goalID, revision)
	if err != nil {
		return model.GoalTermination{}, err
	}
	if term == nil {
		return model.GoalTermination{}, errors.New("no terminal state is recorded")
	}
	return *term, nil
}

func (a *runtimeControlAuthority) CreateCheckpoint(ctx context.Context, runID, taskID, reason string) (execution.CheckpointRecord, error) {
	service, err := a.execution()
	if err != nil {
		return execution.CheckpointRecord{}, err
	}
	return service.CreateCheckpoint(ctx, runID, taskID, reason)
}

func (a *runtimeControlAuthority) Checkpoint(ctx context.Context, checkpointID string) (execution.CheckpointRecord, error) {
	service, err := a.execution()
	if err != nil {
		return execution.CheckpointRecord{}, err
	}
	return service.Checkpoint(ctx, checkpointID)
}

// Checkpoints lists the durable checkpoints of this session's runs.
func (a *runtimeControlAuthority) Checkpoints(ctx context.Context) ([]execution.CheckpointRecord, error) {
	service, err := a.execution()
	if err != nil {
		return nil, err
	}
	engine := service.Engine()
	if engine == nil {
		return nil, errors.New("the execution engine is unavailable")
	}
	runs, err := engine.ListRuns(ctx)
	if err != nil {
		return nil, err
	}
	var records []execution.CheckpointRecord
	for _, run := range runs {
		if run.SessionID != a.sessionID {
			continue
		}
		records = append(records, run.Checkpoints...)
	}
	// Newest first, so the list reads the way a user expects and the default
	// selection is the most recent state rather than the oldest.
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	return records, nil
}

func (a *runtimeControlAuthority) Rollback(ctx context.Context, checkpointID string) (execution.CheckpointRecord, error) {
	service, err := a.execution()
	if err != nil {
		return execution.CheckpointRecord{}, err
	}
	return service.Rollback(ctx, checkpointID)
}

func (a *runtimeControlAuthority) ActiveCanaries(ctx context.Context) ([]optimization.Canary, error) {
	if a.runtime == nil || a.runtime.Optimization() == nil {
		return nil, errNoRuntime
	}
	return a.runtime.Optimization().ActiveCanaries(ctx)
}

func (a *runtimeControlAuthority) Canary(ctx context.Context, canaryID string) (optimization.Canary, error) {
	if a.runtime == nil || a.runtime.Optimization() == nil {
		return optimization.Canary{}, errNoRuntime
	}
	return a.runtime.Optimization().CanaryStatus(ctx, canaryID)
}

func (a *runtimeControlAuthority) RollbackCanary(ctx context.Context, canaryID, reason string) (optimization.Canary, error) {
	if a.runtime == nil || a.runtime.Optimization() == nil {
		return optimization.Canary{}, errNoRuntime
	}
	return a.runtime.Optimization().Rollback(ctx, canaryID, reason)
}

// ApproveGoal records Process 03 confirmation through the canonical boundary.
//
// Runtime.ApproveGoal checks the expected revision itself and writes a new
// CAS-guarded revision, so the whole transition happens inside Process 03's
// own service rather than being assembled here.
func (a *runtimeControlAuthority) ApproveGoal(ctx context.Context, sessionID string, expectedRevision int64) (model.GoalContract, error) {
	if a.runtime == nil {
		return model.GoalContract{}, errNoRuntime
	}
	return a.runtime.ApproveGoal(ctx, sessionID, expectedRevision)
}

func (a *runtimeControlAuthority) ReviseGoal(ctx context.Context, sessionID string, expectedRevision int64, interpretation, reason string) (model.GoalContract, error) {
	if a.runtime == nil {
		return model.GoalContract{}, errNoRuntime
	}
	return a.runtime.ReviseGoal(ctx, sessionID, expectedRevision, interpretation, reason)
}

// CreatePlan builds a Process 04 plan from the confirmed goal.
func (a *runtimeControlAuthority) CreatePlan(ctx context.Context, sessionID string) (PlanState, error) {
	service, err := a.plans()
	if err != nil {
		return PlanState{}, err
	}
	created, err := service.Create(ctx, app.CreatePlanRequest{
		SessionID: sessionID,
		ProjectID: a.projectID,
	})
	if err != nil {
		return PlanState{}, err
	}
	return planState(created), nil
}

// RegisterAgent adds an agent through the canonical runtime.
func (a *runtimeControlAuthority) RegisterAgent(ctx context.Context, name, role string) (model.Agent, error) {
	if a.runtime == nil {
		return model.Agent{}, errNoRuntime
	}
	return a.runtime.RegisterAgent(ctx, app.RegisterAgentRequest{
		Name: name, Role: model.Role(role),
	})
}

// ImportTasks imports a task set through the canonical runtime.
func (a *runtimeControlAuthority) ImportTasks(ctx context.Context, tasks []model.Task) (int, error) {
	if a.runtime == nil {
		return 0, errNoRuntime
	}
	result, err := a.runtime.ImportTasks(ctx, tasks)
	if err != nil {
		return 0, err
	}
	return result.Added, nil
}

// SetAutonomyMode records the session's autonomy preference.
//
// The frozen spec names tui.Workspace as the canonical owner of this
// preference, so it is written there — the same field the prompt and the
// status line read, which is what makes the change observable.
func (a *runtimeControlAuthority) SetAutonomyMode(ctx context.Context, mode string) error {
	if a.setMode == nil {
		return errors.New("no workspace is attached to record the autonomy mode")
	}
	return a.setMode(mode)
}

func (a *runtimeControlAuthority) AutonomyMode(ctx context.Context) (string, error) {
	if a.mode == nil {
		return "", errors.New("no workspace is attached to read the autonomy mode")
	}
	return a.mode(), nil
}

// SetULTRAExecutionPreference records the preference on the workspace.
//
// It is not an authority: the gate decides entitlement, and this flag changes
// nothing without one.
func (a *runtimeControlAuthority) SetULTRAExecutionPreference(ctx context.Context, enabled bool) error {
	if a.setPreference == nil {
		return errors.New("no workspace is attached to record the preference")
	}
	return a.setPreference(enabled)
}

func (a *runtimeControlAuthority) ULTRAExecutionPreference(ctx context.Context) (bool, error) {
	if a.preference == nil {
		return false, errors.New("no workspace is attached to read the preference")
	}
	return a.preference(), nil
}

// RequestULTRA asks the Community Cloud through the canonical client.
//
// There is deliberately no local fallback: if no requester is wired, this
// refuses rather than pretending an entitlement was granted.
func (a *runtimeControlAuthority) RequestULTRA(ctx context.Context) error {
	if a.requestULTRA == nil {
		return errors.New(
			"no Community Cloud client is configured for this installation, so no " +
				"entitlement can be requested")
	}
	return a.requestULTRA(ctx)
}

// ULTRAEntitled asks the canonical gate. A nil gate answers no.
func (a *runtimeControlAuthority) ULTRAEntitled() bool {
	if a == nil || a.gate == nil {
		return false
	}
	gate := a.gate()
	if gate == nil {
		return false
	}
	return gate.Entitled()
}

// --- Work reads ---
//
// The same adapter answers the Work section's reads. Sharing it keeps one set
// of canonical handles rather than two that could disagree about which project
// or session is current.

func (a *runtimeControlAuthority) Layout(ctx context.Context) (WorkLayout, error) {
	if a.runtime == nil {
		return WorkLayout{}, errNoRuntime
	}
	// app.Runtime keeps its layout unexported, so the project row is the
	// canonical source for the project's shape. Reaching into the runtime's
	// internals would be a second view of the same thing.
	out := WorkLayout{ProjectID: string(a.projectID)}
	// The repository and pack version live on the canonical project row rather
	// than on the layout, so they are read from there.
	if a.store != nil {
		if project, err := a.store.Project(ctx); err == nil {
			out.Repository = project.Repository
			out.Branch = project.DefaultBranch
			out.Root = project.Repository
			out.PackVersion = project.PackVersion
			out.Initialized = true
			if out.ProjectID == "" {
				out.ProjectID = project.ID
			}
		} else {
			out.Initialized = false
			out.MissingPieces = append(out.MissingPieces, "canonical project record")
		}
	}
	return out, nil
}

func (a *runtimeControlAuthority) Assessment(ctx context.Context) (startup.Assessment, error) {
	// Readiness is assessed by the startup package at launch. A running
	// workspace has no canonical way to re-run it, so this reports that rather
	// than synthesising an assessment of its own.
	return startup.Assessment{}, errors.New(
		"readiness is assessed when MARSHAL starts; a running workspace cannot re-run it")
}

func (a *runtimeControlAuthority) Goal(ctx context.Context) (model.GoalContract, error) {
	return a.CurrentGoal(ctx)
}

func (a *runtimeControlAuthority) GoalRevisions(ctx context.Context, goalID string) ([]model.GoalContract, error) {
	if a.store == nil {
		return nil, errors.New("no store is attached")
	}
	return a.store.ListGoalRevisions(ctx, goalID)
}

func (a *runtimeControlAuthority) Plan(ctx context.Context) (PlanState, error) {
	return a.CurrentPlan(ctx)
}

func (a *runtimeControlAuthority) ExecutionPlan(ctx context.Context) (plan.ExecutionPlan, error) {
	service, err := a.plans()
	if err != nil {
		return plan.ExecutionPlan{}, err
	}
	return service.Current(ctx, a.projectID)
}

func (a *runtimeControlAuthority) Runs(ctx context.Context) ([]execution.ExecutionRun, error) {
	if a == nil || a.runtime == nil || a.runtime.Execution() == nil {
		return nil, errNoRuntime
	}
	return a.runtime.Execution().ListRuns(ctx)
}

func (a *runtimeControlAuthority) ActiveLease(ctx context.Context, taskID string) (model.ActiveLease, error) {
	if a == nil || a.store == nil {
		return model.ActiveLease{}, errors.New("no store is attached")
	}
	return a.store.ActiveLease(ctx, taskID)
}

func (a *runtimeControlAuthority) Tasks(ctx context.Context) ([]model.Task, error) {
	if a.runtime == nil {
		return nil, errNoRuntime
	}
	return a.runtime.Tasks(ctx)
}

func (a *runtimeControlAuthority) Agents(ctx context.Context) ([]model.Agent, error) {
	if a.runtime == nil {
		return nil, errNoRuntime
	}
	return a.runtime.Agents(ctx)
}

func (a *runtimeControlAuthority) Artifacts(ctx context.Context) ([]model.Artifact, error) {
	if a.runtime == nil {
		return nil, errNoRuntime
	}
	return a.runtime.Artifacts(ctx)
}

func (a *runtimeControlAuthority) Events(ctx context.Context) ([]model.Event, error) {
	if a.runtime == nil {
		return nil, errNoRuntime
	}
	return a.runtime.Events(ctx)
}

// --- Verify: Process 06 ---

func (a *runtimeControlAuthority) verification() (*app.VerificationService, error) {
	if a == nil || a.runtime == nil {
		return nil, errNoRuntime
	}
	service := a.runtime.Verification()
	if service == nil {
		return nil, errors.New("the verification service is unavailable in this runtime")
	}
	return service, nil
}

// StartVerification opens a Process 06 session bound to the run.
//
// StartForRun binds the session to the run's own goal and plan inside the
// canonical service, so the binding cannot be assembled incorrectly here.
func (a *runtimeControlAuthority) StartVerification(ctx context.Context, runID string) (verification.Session, error) {
	service, err := a.verification()
	if err != nil {
		return verification.Session{}, err
	}
	return service.StartForRun(ctx, runID, verification.Session{})
}

func (a *runtimeControlAuthority) EvaluateVerification(ctx context.Context, sessionID string) (verification.Session, error) {
	service, err := a.verification()
	if err != nil {
		return verification.Session{}, err
	}
	return service.Evaluate(ctx, sessionID)
}

func (a *runtimeControlAuthority) Verification(ctx context.Context, sessionID string) (verification.Session, error) {
	service, err := a.verification()
	if err != nil {
		return verification.Session{}, err
	}
	return service.Current(ctx, sessionID)
}

// CurrentVerification reads the session for this workspace's run, for the
// Verify section's reads.
func (a *runtimeControlAuthority) CurrentVerification(ctx context.Context) (verification.Session, error) {
	runID, err := a.CurrentRunID(ctx)
	if err != nil {
		return verification.Session{}, err
	}
	if runID == "" {
		return verification.Session{}, errors.New("no run is active in this session")
	}
	service, err := a.verification()
	if err != nil {
		return verification.Session{}, err
	}
	return service.CurrentForRun(ctx, runID)
}

// Attestation reads the completion attestation, if one exists.
func (a *runtimeControlAuthority) Attestation(ctx context.Context, sessionID string) (verification.CompletionAttestation, error) {
	service, err := a.verification()
	if err != nil {
		return verification.CompletionAttestation{}, err
	}
	return service.Attestation(ctx, sessionID)
}

// --- Work boundaries agy identified as existing ---

// HandoffPlan hands the approved plan to Process 05 through Process 04's own
// boundary, which is the operation the frozen spec names for CTUI-0259.
func (a *runtimeControlAuthority) HandoffPlan(ctx context.Context, sessionID string) (PlanHandoff, error) {
	service, err := a.plans()
	if err != nil {
		return PlanHandoff{}, err
	}
	handoff, err := service.Handoff(ctx, sessionID, a.projectID)
	if err != nil {
		return PlanHandoff{}, err
	}
	// The handoff has no id of its own; it is identified by the plan and
	// version it carries, which is what a later reader would look up.
	return PlanHandoff{
		ID:         fmt.Sprintf("%s@v%d", handoff.PlanID, handoff.PlanVersion),
		PlanID:     handoff.PlanID,
		Version:    handoff.PlanVersion,
		Digest:     fmt.Sprintf("%s/%s", handoff.Goal.RequestDigest, handoff.Goal.ConstraintDigest),
		EvidenceID: handoff.EvidenceID,
	}, nil
}

// PlanTasks reads the tasks the current plan defines.
func (a *runtimeControlAuthority) PlanTasks(ctx context.Context) ([]model.Task, error) {
	service, err := a.plans()
	if err != nil {
		return nil, err
	}
	current, err := service.Current(ctx, a.projectID)
	if err != nil {
		return nil, err
	}
	// The plan's own task definitions are what an import carries. They are
	// converted to canonical task rows without adding anything: the plan is
	// the author of this work, and inventing a field here would be the TUI
	// deciding something Process 04 owns.
	tasks := make([]model.Task, 0, len(current.Tasks))
	for _, task := range current.Tasks {
		tasks = append(tasks, model.Task{
			ID:    task.ID,
			Title: task.Title,
			// A newly imported task is proposed until the scheduler readies it.
			Status: model.TaskProposed,
			// The plan's dependency edges travel with the task, because a task
			// imported without them would be schedulable out of order.
			Dependencies: task.DependsOn,
		})
	}
	return tasks, nil
}

// GCWorktrees collects eligible worktrees, or counts them on a dry run.
func (a *runtimeControlAuthority) GCWorktrees(ctx context.Context, dryRun bool) (int, error) {
	if a.runtime == nil {
		return 0, errNoRuntime
	}
	result, err := a.runtime.GCWorktrees(ctx, dryRun, 0)
	if err != nil {
		return 0, err
	}
	return len(result.CleanedPaths), nil
}

// --- Memory: Process 07 ---

func (a *runtimeControlAuthority) memory() (*app.MemoryService, error) {
	if a == nil || a.runtime == nil {
		return nil, errNoRuntime
	}
	service := a.runtime.Memory()
	if service == nil {
		return nil, errors.New("the memory service is unavailable in this runtime")
	}
	return service, nil
}

func (a *runtimeControlAuthority) RebuildMemoryProjections(ctx context.Context) error {
	service, err := a.memory()
	if err != nil {
		return err
	}
	return service.RebuildProjections(ctx, string(a.projectID))
}

func (a *runtimeControlAuthority) InvalidateMemory(ctx context.Context, memoryID, scopeID string) error {
	service, err := a.memory()
	if err != nil {
		return err
	}
	return service.InvalidateRecord(ctx, string(a.projectID), memoryID, scopeID)
}

func (a *runtimeControlAuthority) MemoryRecord(ctx context.Context, memoryID string) (model.MemoryRecordV2, error) {
	service, err := a.memory()
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return service.Get(ctx, a.memoryPrincipal(), string(a.projectID), memoryID)
}

// MemoryStatus reports the service's own health, for the Memory section reads.
func (a *runtimeControlAuthority) MemoryStatus(ctx context.Context) (MemoryStatus, error) {
	service, err := a.memory()
	if err != nil {
		return MemoryStatus{}, err
	}
	status, err := service.Status(ctx, string(a.projectID))
	if err != nil {
		return MemoryStatus{}, err
	}
	return MemoryStatus{
		Version: status.Version, Healthy: status.Healthy, ProjectID: status.ProjectID,
	}, nil
}

// RecallRecent lists recent records.
func (a *runtimeControlAuthority) RecallRecent(ctx context.Context, limit int) ([]model.MemoryRecordV2, error) {
	service, err := a.memory()
	if err != nil {
		return nil, err
	}
	return service.ListRecent(ctx, a.memoryPrincipal(), string(a.projectID), limit)
}

func (a *runtimeControlAuthority) memoryPrincipal() authz.Principal {
	return authz.Principal{
		ID: a.sessionID,
		// The session is an operator principal, not a synthetic role name.
		// Authorities remain explicit; the TUI does not grant itself any.
		Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}},
	}
}

func (a *runtimeControlAuthority) Remember(ctx context.Context, title, body string) (model.MemoryRecordV2, error) {
	service, err := a.memory()
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return service.Remember(ctx, a.memoryPrincipal(), app.RememberRequest{
		ProjectID: string(a.projectID), ScopeID: string(a.projectID),
		Title: title, Body: body, Kind: model.MemoryKindSemantic,
	})
}

func (a *runtimeControlAuthority) CaptureOutcome(ctx context.Context, taskID, status, failureReason string) (model.MemoryRecordV2, error) {
	service, err := a.memory()
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	runID, err := a.CurrentRunID(ctx)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	if runID == "" {
		return model.MemoryRecordV2{}, errors.New("no current Process 05 run")
	}
	run, err := a.GetRun(ctx, runID)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	runTask, ok := run.Tasks[taskID]
	if !ok {
		return model.MemoryRecordV2{}, fmt.Errorf("task %s is not part of run %s", taskID, runID)
	}
	task, err := a.Task(ctx, taskID)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return service.CaptureOutcome(ctx, app.OutcomeCaptureRequest{
		ProjectID: string(a.projectID), TaskID: taskID, TaskTitle: task.Title,
		RunID: runID, SessionID: a.sessionID, AgentID: runTask.AssignedAgent,
		Provider: runTask.AssignedHarness, Status: status,
		HeadCommit: "", WorktreeID: runTask.WorktreePath,
		EvidenceIDs:   append([]string(nil), runTask.CollectedEvidence...),
		FilesChanged:  append([]string(nil), runTask.TargetFiles...),
		FailureReason: failureReason,
	})
}

// TaskSlots lists working-memory slots for a task.
func (a *runtimeControlAuthority) TaskSlots(ctx context.Context, taskID string) ([]MemorySlot, error) {
	return nil, errors.New(
		"listing working-memory slots requires a principal this screen does not hold")
}

// --- Models: Process 08 reads ---

func (a *runtimeControlAuthority) optimization() (*app.OptimizationService, error) {
	if a == nil || a.runtime == nil {
		return nil, errNoRuntime
	}
	service := a.runtime.Optimization()
	if service == nil {
		return nil, errors.New("the optimization service is unavailable in this runtime")
	}
	return service, nil
}

// Cycles lists Process 08 cycles.
//
// The service reads a cycle by id rather than listing them, so this reports
// that rather than scanning the store behind its back.
func (a *runtimeControlAuthority) Cycles(ctx context.Context) ([]OptimizationCycle, error) {
	service, err := a.optimization()
	if err != nil {
		return nil, err
	}
	cycles, err := service.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]OptimizationCycle, 0, len(cycles))
	for _, cycle := range cycles {
		candidate := ""
		if len(cycle.Candidates) > 0 {
			candidate = cycle.Candidates[0].ID
		}
		// A durable Cycle has no terminal-state field. Presence proves it started,
		// not that it completed, so expose the weakest truthful lifecycle state.
		status := "STARTED"
		if len(cycle.BlockedOptimization) > 0 {
			status = "PENDING"
		}
		out = append(out, OptimizationCycle{
			ID: cycle.ID, Status: status, StartedAt: cycle.CreatedAt, Candidate: candidate,
		})
	}
	return out, nil
}

func (a *runtimeControlAuthority) CanariesFor(ctx context.Context, cycleID string) ([]OptimizationCanary, error) {
	service, err := a.optimization()
	if err != nil {
		return nil, err
	}
	canaries, err := service.Canaries(ctx, cycleID)
	if err != nil {
		return nil, err
	}
	out := make([]OptimizationCanary, 0, len(canaries))
	for _, canary := range canaries {
		out = append(out, OptimizationCanary{
			ID:     canary.ID,
			Status: string(canary.State),
		})
	}
	return out, nil
}

func (a *runtimeControlAuthority) CycleEvidence(ctx context.Context, cycleID string) (OptimizationEvidence, error) {
	service, err := a.optimization()
	if err != nil {
		return OptimizationEvidence{}, err
	}
	candidates, err := service.Candidates(ctx, cycleID)
	if err != nil {
		return OptimizationEvidence{}, err
	}
	counterfactuals, err := service.Counterfactuals(ctx, cycleID)
	if err != nil {
		return OptimizationEvidence{}, err
	}
	manifests, err := service.Manifests(ctx, cycleID)
	if err != nil {
		return OptimizationEvidence{}, err
	}
	summary := OptimizationEvidence{
		Candidates: len(candidates), Counterfactuals: len(counterfactuals), Benchmarks: len(manifests),
	}
	for _, candidate := range candidates {
		results, readErr := service.ExperimentResults(ctx, cycleID, candidate.ID)
		if readErr != nil {
			return OptimizationEvidence{}, readErr
		}
		promotions, readErr := service.PromotionRecords(ctx, cycleID, candidate.ID)
		if readErr != nil {
			return OptimizationEvidence{}, readErr
		}
		summary.Results += len(results)
		summary.Promotions += len(promotions)
	}
	return summary, nil
}

// LocalModels lists locally available models as the resource collector saw them.
func (a *runtimeControlAuthority) LocalModels(ctx context.Context) ([]LocalModel, error) {
	snapshot := resources.NewCollector().Collect(ctx, "")
	out := make([]LocalModel, 0, len(snapshot.Ollama.Models))
	for _, model := range snapshot.Ollama.Models {
		out = append(out, LocalModel{
			Name:   model.Name,
			Family: model.Family,
			// The collector's own fit assessment and its reason travel
			// together, so a "may fit" is never read as a recommendation.
			Compatibility: string(model.Compatibility),
			Reason:        model.Reason,
		})
	}
	return out, nil
}

// --- Security ---

func (a *runtimeControlAuthority) RevokeGrant(ctx context.Context, bindingID string) error {
	if a.store == nil {
		return errors.New("no store is attached")
	}
	// The store records the revocation rather than deleting the row, so the
	// audit trail of who held what survives the withdrawal.
	return a.store.RevokeRoleBinding(ctx, bindingID, time.Now().UTC())
}

func (a *runtimeControlAuthority) Grant(ctx context.Context, bindingID string) (authz.RoleBinding, error) {
	if a.store == nil {
		return authz.RoleBinding{}, errors.New("no store is attached")
	}
	return a.store.GetRoleBinding(ctx, bindingID)
}

// ConstitutionVersion reports the constitution this build enforces.
func (a *runtimeControlAuthority) ConstitutionVersion(ctx context.Context) (string, error) {
	return constitution.Current.String(), nil
}

// RoleBindings lists capability and role grants.
func (a *runtimeControlAuthority) RoleBindings(ctx context.Context) ([]authz.RoleBinding, error) {
	// The store reads a binding by id and answers membership questions, but
	// exposes no listing. Saying so is honest; scanning the table behind its
	// back would be a second query path that could disagree with the checks
	// the authorizer actually makes.
	return nil, errors.New(
		"the authorization store answers membership questions and reads a " +
			"binding by id, but exposes no grant listing, so this build cannot " +
			"enumerate who holds access")
}

// SandboxState reports whether isolation is enforceable here.
func (a *runtimeControlAuthority) SandboxState(ctx context.Context) (SandboxState, error) {
	if a.runtime == nil {
		return SandboxState{}, errNoRuntime
	}
	// The runtime keeps its sandbox and egress predicates unexported, so this
	// process cannot observe them. That is reported rather than guessed: the
	// safe reading of "I cannot tell whether isolation is enforced" is not
	// "it is", and the section renders it as unverified accordingly.
	return SandboxState{}, errors.New(
		"the runtime does not expose its sandbox or egress enforcement state to " +
			"this process, so isolation cannot be confirmed from here")
}

// SecretLeases counts held leases without reading any of them.
func (a *runtimeControlAuthority) SecretLeases(ctx context.Context) (int, error) {
	// Lease content stays on the execution host by design, and the broker
	// exposes no count, so this reports that rather than guessing zero — which
	// would read as "no secrets are in play".
	return 0, errors.New(
		"the secret broker exposes no lease count; leases are held on the " +
			"execution host and are not enumerable from this process")
}

// --- System reads ---

// RuntimeStatus adapts app.Status to the deliberately small System reader
// shape.  It is a read only projection; it does not manufacture health from a
// successful method call.
func (a *runtimeControlAuthority) RuntimeStatus(ctx context.Context) (RuntimeStatus, error) {
	if a == nil || a.runtime == nil {
		return RuntimeStatus{}, errNoRuntime
	}
	status, err := a.runtime.Status(ctx)
	if err != nil {
		return RuntimeStatus{}, err
	}
	return RuntimeStatus{ProjectID: status.Project.ID, ProjectName: status.Project.Repository,
		SchemaVersion: status.SchemaVersion, AgentCount: status.AgentCount,
		SessionCount: status.SessionCount, TaskCount: status.TaskCount,
		LeaseCount: status.LeaseCount}, nil
}

func (a *runtimeControlAuthority) RuntimeInstanceID() string {
	if a == nil || a.runtime == nil {
		return ""
	}
	return a.runtime.InstanceID()
}

func (a *runtimeControlAuthority) StoreSchemaVersion(ctx context.Context) (int, error) {
	if a == nil || a.store == nil {
		return 0, errors.New("no store is attached")
	}
	return a.store.SchemaVersion(ctx)
}

func (a *runtimeControlAuthority) StoreIntegrity(ctx context.Context) error {
	if a == nil || a.store == nil {
		return errors.New("no store is attached")
	}
	return a.store.Integrity(ctx)
}

func (a *runtimeControlAuthority) ObjectCount(ctx context.Context, table string) (int, error) {
	if a == nil || a.store == nil {
		return 0, errors.New("no store is attached")
	}
	return a.store.Count(ctx, table)
}

func (a *runtimeControlAuthority) CollectResources(ctx context.Context) (resources.Snapshot, error) {
	// Collector.Collect is deliberately read-only.  It does not require a
	// runtime path, and an empty state path avoids disclosing project paths.
	return resources.NewCollector().Collect(ctx, ""), nil
}

func (a *runtimeControlAuthority) TokenMetadata(ctx context.Context) ([]auth.TokenRecord, error) {
	if a.runtime == nil {
		return nil, errNoRuntime
	}
	return a.runtime.TokenMetadata(ctx)
}

func (a *runtimeControlAuthority) CreateAccessToken(ctx context.Context, name string, kind auth.PrincipalKind, capabilities []string, idempotencyKey string) (string, auth.TokenRecord, bool, error) {
	if a.runtime == nil {
		return "", auth.TokenRecord{}, false, errNoRuntime
	}
	result, err := a.runtime.CreateAccessToken(ctx, app.TokenCreateRequest{
		Name: name, Kind: kind, Capabilities: capabilities, IdempotencyKey: idempotencyKey,
	})
	return result.Plaintext, result.Record, result.Created, err
}

func (a *runtimeControlAuthority) RevokeAccessToken(ctx context.Context, id string) error {
	if a.runtime == nil {
		return errNoRuntime
	}
	return a.runtime.RevokeAccessToken(ctx, id)
}

func (a *runtimeControlAuthority) RequestWorkspaceExit(context.Context) error {
	if a == nil || a.requestExit == nil {
		return errors.New("workspace exit boundary is unavailable")
	}
	return a.requestExit()
}

func (a *runtimeControlAuthority) WorkspaceExitRequested() bool {
	return a != nil && a.exitRequested != nil && a.exitRequested()
}

// --- System ---

// BackupState writes a durable backup through the canonical runtime.
//
// The output path is left to the runtime, which chooses one inside its own
// directory. Passing a path from here would put the runtime's layout into the
// TUI and risk disclosing it on screen.
func (a *runtimeControlAuthority) BackupState(ctx context.Context) (BackupProof, error) {
	if a.runtime == nil {
		return BackupProof{}, errNoRuntime
	}
	metadata, err := a.runtime.BackupState(ctx, "")
	if err != nil {
		return BackupProof{}, err
	}
	return BackupProof{
		Digest:        metadata.DatabaseSHA256,
		SchemaVersion: metadata.SchemaVersion,
		CreatedAt:     metadata.CreatedAt,
	}, nil
}

// VerifyStateBackup verifies the operator-selected backup against this exact
// project before Control shows a destructive confirmation. The host path stays
// within this authority and is never placed in a screen snapshot.
func (a *runtimeControlAuthority) VerifyStateBackup(ctx context.Context, backupPath string) (BackupProof, error) {
	if a == nil || a.runtime == nil {
		return BackupProof{}, errNoRuntime
	}
	if a.projectID == "" {
		return BackupProof{}, errors.New("restore requires a canonical project identity")
	}
	schema, err := a.runtime.Store().SchemaVersion(ctx)
	if err != nil {
		return BackupProof{}, err
	}
	metadata, err := app.VerifyStateBackup(ctx, backupPath, string(a.projectID), schema)
	if err != nil {
		return BackupProof{}, err
	}
	return BackupProof{Digest: metadata.DatabaseSHA256, SchemaVersion: metadata.SchemaVersion, CreatedAt: metadata.CreatedAt}, nil
}

// RestoreState closes the live runtime before replacing its database, invokes
// the canonical restore operation, then reopens the runtime. Restoring over an
// open SQLite handle would leave the UI reading an unlinked old database, so
// the stopped-runtime boundary is part of the authority rather than a UI
// convention.
func (a *runtimeControlAuthority) RestoreState(ctx context.Context, backupPath string, expected BackupProof) (BackupProof, error) {
	if a == nil || a.runtime == nil {
		return BackupProof{}, errNoRuntime
	}
	if a.projectID == "" {
		return BackupProof{}, errors.New("restore requires a canonical project identity")
	}
	verified, err := a.VerifyStateBackup(ctx, backupPath)
	if err != nil {
		return BackupProof{}, err
	}
	// The second verification is not merely an integrity check. It must still
	// be the exact backup whose digest and schema the destructive confirmation
	// displayed. Otherwise a path swap between confirmation and submit could
	// restore a different, perfectly valid database.
	if expected.Digest == "" || expected.SchemaVersion <= 0 ||
		verified.Digest != expected.Digest || verified.SchemaVersion != expected.SchemaVersion {
		return BackupProof{}, fmt.Errorf("%w: selected backup no longer matches the confirmed digest/schema", ErrStaleTarget)
	}
	old := a.runtime
	root := old.ProjectRoot()
	if root == "" {
		return BackupProof{}, errors.New("runtime has no project root")
	}
	if err := old.Close(); err != nil {
		return BackupProof{}, fmt.Errorf("stop runtime for restore: %w", err)
	}
	if err := app.RestoreStateForProjectExpected(ctx, root, backupPath, string(a.projectID), verified.Digest); err != nil {
		// RestoreDatabase keeps the old database intact on preflight/copy
		// failures. Reopen it so this workspace remains usable after refusal.
		if reopened, reopenErr := app.Open(ctx, root); reopenErr == nil {
			if a.replaceRuntime != nil {
				a.replaceRuntime(reopened)
			}
		}
		return BackupProof{}, fmt.Errorf("restore verified backup: %w", err)
	}
	reopened, err := app.Open(ctx, root)
	if err != nil {
		return BackupProof{}, fmt.Errorf("reopen runtime after restore: %w", err)
	}
	if a.replaceRuntime != nil {
		a.replaceRuntime(reopened)
	}
	// Re-verify after reopening so success means the durable replacement is
	// both valid and readable through the new canonical runtime.
	schema, err := reopened.Store().SchemaVersion(ctx)
	if err != nil {
		return BackupProof{}, fmt.Errorf("read reopened runtime schema: %w", err)
	}
	metadata, err := app.VerifyStateBackup(ctx, backupPath, string(a.projectID), schema)
	if err != nil {
		return BackupProof{}, fmt.Errorf("verify restored runtime: %w", err)
	}
	proof := BackupProof{Digest: metadata.DatabaseSHA256, SchemaVersion: metadata.SchemaVersion, CreatedAt: metadata.CreatedAt}
	if proof.Digest != verified.Digest || proof.SchemaVersion != verified.SchemaVersion {
		return BackupProof{}, errors.New("restored backup proof changed during restore")
	}
	return proof, nil
}

// GCArtifacts collects eligible artifacts, or counts them on a dry run.
func (a *runtimeControlAuthority) GCArtifacts(ctx context.Context, dryRun bool) (int, error) {
	if a.runtime == nil {
		return 0, errNoRuntime
	}
	// TTL and budget are left at their zero values so the runtime applies its
	// own policy defaults; supplying numbers here would make this screen the
	// author of a retention policy it does not own.
	result, err := a.runtime.GCArtifacts(ctx, dryRun, 0, 0)
	if err != nil {
		return 0, err
	}
	return len(result.CleanedFiles), nil
}
