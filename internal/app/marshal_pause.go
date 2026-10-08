package app

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func pauseMarshal(run *marshal.Run, reason, resolution string, resume marshal.RunState) {
	run.State = marshal.AwaitingUser
	run.Pause = &marshal.Pause{Reason: reason, Resolutions: []string{resolution}, ResumeState: resume}
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
		return fmt.Sprintf("marshal/%s/r%d/integration", runID, run.ArtifactRevision)
	}
	return "marshal/" + runID + "/integration"
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
