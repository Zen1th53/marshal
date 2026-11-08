package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func (s *MarshalService) pauseVerificationFailure(ctx context.Context, runID string, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return errors.Join(cause, err)
	}
	if run.State != marshal.Verifying {
		return cause
	}
	pauseMarshal(&run, "verification stopped: "+cause.Error(),
		"inspect the retained verification error and artifacts; resolve the reported cause, then /marshal resume to rerun verification; if historical Process05 proof is missing, create and approve a new plan and use /marshal use-plan", marshal.Verifying)
	if err := s.save(ctx, runID, run, rev); err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(cause, s.record(ctx, runID, "", events.EventTypeMarshalEscalated, map[string]any{"reason": run.Pause.Reason, "role": "verification", "resolutions": run.Pause.Resolutions}))
}

func pauseMarshal(run *marshal.Run, reason, resolution string, resume marshal.RunState) {
	run.State = marshal.AwaitingUser
	run.Pause = &marshal.Pause{Reason: reason, Resolutions: []string{resolution}, ResumeState: resume}
}

// Cancellation leaves approved criteria unresolved, including on runs written
// by older runtimes that treated a cancelled task as completed integration.
func (s *MarshalService) requireUncancelledScope(ctx context.Context, runID string, run marshal.Run, rev int64) error {
	for _, task := range run.Tasks {
		if task.State == marshal.Cancelled {
			reason := "task " + task.PlanTaskID + " cancelled; approved plan scope requires review"
			pauseMarshal(&run, reason, "create and approve an amended plan that removes or replaces the cancelled task and updates its dependencies, then /marshal use-plan", "")
			return errors.Join(errors.New(reason), s.save(context.WithoutCancel(ctx), runID, run, rev))
		}
	}
	return nil
}

func marshalPauseError(run marshal.Run) error {
	if run.Pause == nil {
		return fmt.Errorf("run is awaiting operator intervention; resolve the escalation or amend the plan")
	}
	return fmt.Errorf("run paused: %s; next step: %s", run.Pause.Reason, strings.Join(run.Pause.Resolutions, "; "))
}

func integrationTaskID(runID string, run marshal.Run) string {
	if run.ArtifactRevision > 0 {
		return fmt.Sprintf("TASK-%s-r%d-integration", runID, run.ArtifactRevision)
	}
	return "TASK-" + runID + "-integration"
}
func integrationBranch(runID string, run marshal.Run) string {
	if run.ArtifactRevision > 0 {
		return fmt.Sprintf(statepath.RefName+"/%s/r%d/integration", runID, run.ArtifactRevision)
	}
	return statepath.RefName + "/" + runID + "/integration"
}

// marshalTaskBranch is shared by initial planning and revised artifacts. L2
// Process05 admission must validate the stored task using this same identity.
func marshalTaskBranch(runID, taskID string, revision int64) string {
	if revision > 0 {
		return fmt.Sprintf(statepath.RefName+"/%s/r%d/%s", runID, revision, taskID)
	}
	return statepath.RefName + "/" + runID + "/" + taskID
}

func validateMarshalTaskIdentity(runID string, t marshal.Task) error {
	parts := strings.Split(t.Branch, "/")
	if !marshalIdentifier(runID) || !marshalIdentifier(t.PlanTaskID) {
		return fmt.Errorf("invalid Marshal task identity")
	}
	branch := t.Branch
	if statepath.LegacyRefName != "" && strings.HasPrefix(branch, statepath.LegacyRefName+"/") {
		// A run started before the prefix was renamed keeps its branches.
		branch = statepath.RefName + strings.TrimPrefix(branch, statepath.LegacyRefName)
		parts = strings.Split(branch, "/")
	}
	if branch == marshalTaskBranch(runID, t.PlanTaskID, 0) {
		return nil
	}
	if len(parts) == 4 {
		var revision int64
		if _, err := fmt.Sscanf(parts[2], "r%d", &revision); err == nil && revision > 0 && branch == marshalTaskBranch(runID, t.PlanTaskID, revision) {
			return nil
		}
	}
	return fmt.Errorf("process 05: invalid Marshal task branch for run %s task %s", runID, t.PlanTaskID)
}

func taskArtifactID(runID string, t marshal.Task) string {
	parts := strings.Split(t.Branch, "/")
	if len(parts) == 4 {
		return worktreeTaskID(runID, parts[2]+"-"+t.PlanTaskID)
	}
	return worktreeTaskID(runID, t.PlanTaskID)
}

func pauseMarshalBudget(run *marshal.Run, result marshal.BudgetResult) {
	var reasons []string
	if len(result.PlanExceeded) > 0 {
		reasons = append(reasons, "plan budget exceeded: "+strings.Join(result.PlanExceeded, ", "))
	}
	var unknown []string
	for _, name := range result.Unknown {
		if (name == "tokens" && (run.Budget.Tokens.Task > 0 || run.Budget.Tokens.Plan > 0)) || (name == "money" && (run.Budget.Money.Task > 0 || run.Budget.Money.Plan > 0)) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		reasons = append(reasons, "configured budget cannot be evaluated; usage unknown: "+strings.Join(unknown, ", "))
	}
	pauseMarshal(run, strings.Join(reasons, "; "), "start a new run with updated budget settings; settings do not change this run", "")
}
