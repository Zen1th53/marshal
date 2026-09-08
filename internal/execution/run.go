package execution

import (
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/plan"
)

// NewRunFromHandoff initializes a new ExecutionRun from a validated Process 04 Handoff.
func NewRunFromHandoff(handoff plan.Handoff, now time.Time, prov RunProvenance) (ExecutionRun, error) {
	if err := handoff.Validate(); err != nil {
		return ExecutionRun{}, fmt.Errorf("%w: handoff validation failed: %v", ErrRunInvalid, err)
	}

	runID := fmt.Sprintf("run-%s-%d", handoff.PlanID, now.UnixNano())
	tasks := make(map[string]TaskExecution, len(handoff.Tasks))

	for _, t := range handoff.Tasks {
		route := handoff.Routes[t.ID]
		assignmentRole := string(t.Role)
		if assignmentRole == "" {
			assignmentRole = route.Provider
		}
		if assignmentRole == "" {
			assignmentRole = "worker-" + t.ID
		}
		var reqEvidence []string
		for _, v := range handoff.Verification.Obligations {
			for _, tid := range v.Tasks {
				if tid == t.ID {
					reqEvidence = append(reqEvidence, v.Criterion)
					break
				}
			}
		}
		var outputArtifacts []string
		if t.ExpectedOutput != "" {
			outputArtifacts = append(outputArtifacts, t.ExpectedOutput)
		}
		tasks[t.ID] = TaskExecution{
			TaskID:            t.ID,
			Description:       t.Title,
			AssignedRole:      assignmentRole,
			AssignedHarness:   route.Provider,
			AssignedModel:     route.Model,
			State:             TaskPending,
			Dependencies:      append([]string(nil), t.DependsOn...),
			Mutates:           t.Mutating,
			TargetFiles:       append([]string(nil), t.Paths...),
			RequiredEvidence:  reqEvidence,
			OutputArtifacts:   outputArtifacts,
			Attempts:          0,
			UpdatedAt:         now,
		}
	}

	// Check which tasks require approvals from the plan
	for _, app := range handoff.Approvals {
		if te, exists := tasks[app.Task]; exists {
			te.ApprovalRequired = true
			tasks[app.Task] = te
		}
	}

	// Check which tasks have checkpoints before them
	for _, cp := range handoff.Checkpoints {
		if te, exists := tasks[cp.AfterTask]; exists {
			te.CheckpointsBefore = append(te.CheckpointsBefore, cp.AfterTask)
			tasks[cp.AfterTask] = te
		}
	}

	mode := handoff.Mode
	if mode == "" {
		mode = plan.ModeStandard
	}

	run := ExecutionRun{
		RunID:               runID,
		Version:             1,
		ProjectID:           handoff.ProjectID,
		SessionID:           handoff.Goal.GoalID,
		GoalID:              handoff.Goal.GoalID,
		GoalRevision:        handoff.Goal.Revision,
		PlanID:              handoff.PlanID,
		PlanVersion:         handoff.PlanVersion,
		State:               RunReady,
		Mode:                mode,
		CurrentPhase:        PhaseInit,
		ConstitutionVersion: handoff.ConstitutionVersion,
		Tasks:               tasks,
		ActiveWorkers:       make(map[string]WorkerDescriptor),
		Leases:              make(map[string]Lease),
		Approvals:           make(map[string]RuntimeApproval),
		Checkpoints:         make([]CheckpointRecord, 0),
		Evidence:            make([]EvidenceRef, 0),
		BudgetConsumed:      BudgetUsage{},
		ProviderCalls:       make([]ProviderCallMeta, 0),
		Failures:            make([]RunFailure, 0),
		Handoffs:            make([]TypedHandoff, 0),
		OriginalRequest:     handoff.OriginalRequest,
		HardConstraints:     append([]string(nil), handoff.HardConstraints...),
		Provenance:          prov,
		StartedAt:           now,
		UpdatedAt:           now,
	}

	return run, nil
}

// Start transitions a READY or PAUSED run to RUNNING.
func (r *ExecutionRun) Start(now time.Time) error {
	if r.State != RunReady && r.State != RunPaused {
		return fmt.Errorf("%w: cannot start run from state %s", ErrInvalidStateTransition, r.State)
	}
	r.State = RunRunning
	r.CurrentPhase = PhaseExecuting
	r.UpdatedAt = now
	return nil
}

// Pause pauses an active run.
func (r *ExecutionRun) Pause(now time.Time) error {
	if r.State != RunRunning && r.State != RunNeedsApproval {
		return fmt.Errorf("%w: cannot pause run from state %s", ErrInvalidStateTransition, r.State)
	}
	r.State = RunPaused
	r.CurrentPhase = PhasePaused
	r.UpdatedAt = now
	return nil
}

// Resume resumes a paused run.
func (r *ExecutionRun) Resume(now time.Time) error {
	if r.State != RunPaused {
		return fmt.Errorf("%w: cannot resume run from state %s", ErrInvalidStateTransition, r.State)
	}
	r.State = RunRunning
	r.CurrentPhase = PhaseExecuting
	r.UpdatedAt = now
	return nil
}

// Block marks the run blocked due to a missing prerequisite or fatal policy denial.
func (r *ExecutionRun) Block(reason string, now time.Time) error {
	if r.State.IsTerminal() {
		return fmt.Errorf("%w: cannot block terminal run", ErrInvalidStateTransition)
	}
	r.State = RunBlocked
	r.CurrentPhase = PhaseTerminated
	r.UpdatedAt = now
	r.Failures = append(r.Failures, RunFailure{
		Stage:       string(r.CurrentPhase),
		Reason:      reason,
		Recoverable: false,
		Timestamp:   now,
	})
	return nil
}

// Cancel transitions the run to CANCELLED.
func (r *ExecutionRun) Cancel(now time.Time) error {
	if r.State == RunCancelled {
		return nil
	}
	r.State = RunCancelled
	r.CurrentPhase = PhaseTerminated
	r.UpdatedAt = now
	r.EndedAt = &now
	return nil
}

// Fail transitions the run to FAILED.
func (r *ExecutionRun) Fail(reason string, now time.Time) error {
	r.State = RunFailed
	r.CurrentPhase = PhaseTerminated
	r.UpdatedAt = now
	r.EndedAt = &now
	r.Failures = append(r.Failures, RunFailure{
		Stage:       string(r.CurrentPhase),
		Reason:      reason,
		Recoverable: false,
		Timestamp:   now,
	})
	return nil
}

// CompletePendingVerification transitions the run to DONE_PENDING_VERIFICATION.
// Invariant: This is NOT final Goal completion; Process 06 must perform verification.
func (r *ExecutionRun) CompletePendingVerification(now time.Time) error {
	canComplete, unfulfilled := r.CanComplete()
	if !canComplete {
		return fmt.Errorf("%w: cannot complete run: %v", ErrInvalidStateTransition, unfulfilled)
	}
	r.State = RunDonePendingVerification
	r.CurrentPhase = PhaseCompletedPending
	r.UpdatedAt = now
	r.EndedAt = &now
	return nil
}

// CanComplete checks whether all required tasks and obligations are met for handoff to Process 06.
func (r *ExecutionRun) CanComplete() (bool, []string) {
	var unfulfilled []string
	if len(r.Tasks) == 0 {
		return false, []string{"no tasks in run"}
	}

	for id, task := range r.Tasks {
		if task.State != TaskCompletedPendingVerify {
			unfulfilled = append(unfulfilled, fmt.Sprintf("task %s is in state %s, want COMPLETED_PENDING_VERIFY", id, task.State))
		}
	}

	// Check if any approval is pending
	for id, app := range r.Approvals {
		if app.Status == ApprovalRequested {
			unfulfilled = append(unfulfilled, fmt.Sprintf("approval %s for task %s is still pending", id, app.TaskID))
		}
	}

	return len(unfulfilled) == 0, unfulfilled
}
