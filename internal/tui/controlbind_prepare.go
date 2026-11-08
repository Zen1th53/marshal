package tui

// Control bindings: preparation, reading the exact canonical target.
// Split mechanically from controlbind.go; see that file's header for the
// binding contract.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/optimization"
)

// --- preparation: reading the exact canonical target ---

func (s *ControlSource) prepareRun(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	runID, err := s.Authority.CurrentRunID(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("no current run could be identified: %w", err)
	}
	if runID == "" {
		return Target{}, errors.New(
			"no run is active in this session; start an approved plan run first")
	}
	run, err := s.Authority.GetRun(ctx, runID)
	if err != nil {
		return Target{}, fmt.Errorf("the run could not be read: %w", err)
	}
	// Version is the canonical CAS concurrency version, which is exactly what
	// a revision-bound mutation must carry.
	return Target{
		Kind:     "run",
		ID:       run.RunID,
		Revision: run.Version,
		Digest:   string(run.CurrentPhase),
		Scope:    fmt.Sprintf("run %s in session %s", run.RunID, s.SessionID),
		Summary: fmt.Sprintf("run %s v%d, state %s, phase %s, %d task(s)",
			run.RunID, run.Version, run.State, run.CurrentPhase, len(run.Tasks)),
	}, nil
}

func (s *ControlSource) prepareClaimTask(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	tasks, err := s.Authority.Tasks(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("tasks could not be read: %w", err)
	}
	agents, err := s.Authority.Agents(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("agents could not be read: %w", err)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	var task model.Task
	for _, candidate := range tasks {
		if candidate.Status == model.TaskReady {
			task = candidate
			break
		}
	}
	if task.ID == "" {
		return Target{}, errors.New("the canonical ready-task queue is empty")
	}
	var agent model.Agent
	for _, candidate := range agents {
		if candidate.Status != model.AgentDisabled && (candidate.ID == s.ApproverID || agent.ID == "") {
			agent = candidate
			if candidate.ID == s.ApproverID {
				break
			}
		}
	}
	if agent.ID == "" {
		return Target{}, errors.New("no registered eligible agent is available to own the task")
	}
	return taskTarget(task, agent.ID, fmt.Sprintf("task %s assigned to agent %s", task.ID, agent.ID)), nil
}

func (s *ControlSource) prepareLeasedTask(ctx context.Context) (Target, error) {
	return s.prepareTaskByState(ctx, "leased", func(task model.Task) bool {
		return task.Status == model.TaskClaimed || task.Status == model.TaskWorking
	})
}

func (s *ControlSource) prepareCancellableTask(ctx context.Context) (Target, error) {
	return s.prepareTaskByState(ctx, "non-terminal", func(task model.Task) bool {
		return task.Status != model.TaskMerged && task.Status != model.TaskCancelled && task.Status != model.TaskSuperseded
	})
}

func (s *ControlSource) prepareTaskByState(ctx context.Context, description string, eligible func(model.Task) bool) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	tasks, err := s.Authority.Tasks(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("tasks could not be read: %w", err)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	for _, task := range tasks {
		if eligible(task) {
			actor := ""
			if task.OwnerAgentID != nil {
				actor = *task.OwnerAgentID
			}
			return taskTarget(task, actor, fmt.Sprintf("task %s (%s)", task.ID, task.Status)), nil
		}
	}
	return Target{}, fmt.Errorf("no %s canonical task is available", description)
}

func taskTarget(task model.Task, actorID, summary string) Target {
	return Target{
		Kind:     "task",
		ID:       task.ID,
		ActorID:  actorID,
		Revision: task.Revision,
		Digest:   string(task.Status),
		Scope:    fmt.Sprintf("task %s", task.ID),
		Summary:  fmt.Sprintf("%s at revision %d", summary, task.Revision),
	}
}

// actionableTask picks the task a Control action would act on.
//
// The order is deterministic rather than map order, because a confirmation that
// named a different task each time it was opened would make the displayed
// target meaningless.
func actionableTask(run execution.ExecutionRun) string {
	ids := make([]string, 0, len(run.Tasks))
	for id := range run.Tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if run.Tasks[id].State.IsTerminal() {
			continue
		}
		return id
	}
	return ""
}

func (s *ControlSource) preparePlan(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	planState, err := s.Authority.CurrentPlan(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("the current plan could not be read: %w", err)
	}
	if planState.PlanID == "" {
		return Target{}, errors.New("no plan exists for this project yet")
	}
	return Target{
		Kind:     "plan",
		ID:       planState.PlanID,
		Revision: planState.Version,
		Digest:   planState.Digest,
		Scope:    fmt.Sprintf("plan %s in project %s", planState.PlanID, s.ProjectID),
		Summary: fmt.Sprintf("plan %s version %d (%s)",
			planState.PlanID, planState.Version, planState.Status),
	}, nil
}

func (s *ControlSource) prepareGoal(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	goal, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("the active goal could not be read: %w", err)
	}
	if goal.ID == "" {
		return Target{}, errors.New("no goal is active in this session")
	}
	if goal.Confirmation == model.ConfirmationCancelled {
		return Target{}, fmt.Errorf("goal %s is already cancelled", goal.ID)
	}
	return Target{
		Kind:     "goal",
		ID:       goal.ID,
		Revision: goal.Revision,
		// The request digest binds the goal to the exact text it came from, so
		// a goal that was revised shows a different digest.
		Digest:  goal.RequestDigest,
		Scope:   fmt.Sprintf("goal %s in session %s", goal.ID, s.SessionID),
		Summary: fmt.Sprintf("goal %s revision %d (%s)", goal.ID, goal.Revision, goal.Confirmation),
	}, nil
}

func (s *ControlSource) prepareApproval(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	pending, err := s.Authority.PendingApprovals(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("the approval queue could not be read: %w", err)
	}
	if len(pending) == 0 {
		return Target{}, errors.New("no approval is awaiting a decision")
	}
	// The first pending approval is the target. Which one is selected is the
	// caller's business; this reads whichever it is told about through
	// SelectedApprovalID when one is set.
	approval := pending[0]
	if selected, _, _ := s.selection(); selected != "" {
		found := false
		for _, candidate := range pending {
			if candidate.ApprovalID == selected {
				approval, found = candidate, true
				break
			}
		}
		if !found {
			return Target{}, fmt.Errorf(
				"approval %s is no longer pending; it may have been decided or expired",
				selected)
		}
	}
	return approvalTarget(approval), nil
}

// approvalTarget binds the exact action, target, scope, digest and revision the
// governance contract requires an approval to carry.
func approvalTarget(a *execution.RuntimeApproval) Target {
	return Target{
		Kind:     "approval",
		ID:       a.ApprovalID,
		Revision: a.PlanVersion,
		// The action digest is what makes this approval about one exact
		// operation. Approving a digest that has since changed is the TOCTOU
		// failure the contract names.
		Digest: approvalDigest(a),
		Scope:  a.Scope,
		Summary: fmt.Sprintf("%s on %s (risk %s, plan %s v%d)",
			a.OperationType, a.TargetResource, a.RiskLevel, a.PlanID, a.PlanVersion),
	}
}

func approvalDigest(a *execution.RuntimeApproval) string {
	if a == nil {
		return ""
	}
	return a.ActionDigest + "/" + a.StateDigest
}

func (s *ControlSource) prepareCheckpoint(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	checkpoints, err := s.Authority.Checkpoints(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("checkpoints could not be listed: %w", err)
	}
	if len(checkpoints) == 0 {
		return Target{}, errors.New("no durable checkpoint exists to roll back to")
	}
	record := checkpoints[0]
	if _, selected, _ := s.selection(); selected != "" {
		found := false
		for _, candidate := range checkpoints {
			if candidate.CheckpointID == selected {
				record, found = candidate, true
				break
			}
		}
		if !found {
			return Target{}, fmt.Errorf(
				"checkpoint %s is no longer listed", selected)
		}
	}
	return Target{
		Kind:     "checkpoint",
		ID:       record.CheckpointID,
		Revision: record.CreatedAt.UnixNano(),
		Digest:   checkpointDigest(record),
		Scope:    fmt.Sprintf("workspace of run %s", record.RunID),
		Summary: fmt.Sprintf("checkpoint %s taken %s (%s)",
			record.CheckpointID, record.CreatedAt.UTC().Format(time.RFC3339), record.Reason),
	}, nil
}

func checkpointDigest(record execution.CheckpointRecord) string {
	return record.StateDigest + "/" + record.SnapshotDigest
}

func (s *ControlSource) prepareCanary(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	canaries, err := s.Authority.ActiveCanaries(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("active canaries could not be read: %w", err)
	}
	if len(canaries) == 0 {
		return Target{}, errors.New("no pending or running optimization canary is available")
	}
	c := canaries[0]
	return Target{Kind: "canary", ID: c.ID, Revision: c.StartedAt.UnixNano(),
		Digest: canaryDigest(c), Scope: fmt.Sprintf("optimization canary %s", c.ID),
		Summary: fmt.Sprintf("canary %s (%s), exposure %.4g", c.ID, c.State, c.Exposure)}, nil
}

func canaryDigest(c optimization.Canary) string {
	raw, _ := json.Marshal(c)
	return execution.ComputeStateDigest(string(raw))
}

func (s *ControlSource) prepareULTRA(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	mode := "Standard"
	if s.Authority.ULTRAEntitled() {
		mode = "ULTRA"
	}
	return Target{
		Kind:    "session",
		ID:      s.SessionID,
		Digest:  mode,
		Scope:   fmt.Sprintf("session %s", s.SessionID),
		Summary: fmt.Sprintf("session %s currently operating in %s", s.SessionID, mode),
	}, nil
}

// SelectApproval names which approval the approve/reject actions act on.
func (s *ControlSource) SelectApproval(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedApproval = id
}

// SelectGrant names which capability or role binding the revoke action acts on.
func (s *ControlSource) SelectGrant(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedGrantID = id
}

// selectedGrant reads the selection under the lock.
func (s *ControlSource) selectedGrant() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectedGrantID
}

// SelectCheckpoint names which checkpoint the rollback actions act on.
func (s *ControlSource) SelectCheckpoint(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedCheckpoint = id
}

// SetRationale records the operator's stated reason for the next decision.
func (s *ControlSource) SetRationale(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rationale = strings.TrimSpace(text)
}
