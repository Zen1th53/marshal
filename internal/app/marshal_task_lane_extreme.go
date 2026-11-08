//go:build !governed

package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/risk"
)

func (r *Runtime) selectProcess05TaskLane(ctx context.Context, s *MarshalService, run marshal.Run, req driver.Request, parent *execution.ExecutionRun) (*execution.ExecutionRun, error) {
	if serialProcess05Base(s.Gate, run) {
		return parent, nil
	}
	if !run.Process05Bound || req.Task.Mode != marshal.Governed || !marshalDepsMerged(run, req.Task) {
		return nil, fmt.Errorf("%w: task lane requires reviewed dependencies", execution.ErrRunBlocked)
	}
	if err := r.Execution().Engine().SetTaskLaneParent(ctx, parent.RunID, run.BaseCommit); err != nil {
		return nil, err
	}
	return r.Execution().Engine().OpenTaskLaneRework(ctx, parent.RunID, req.Task.PlanTaskID, req.Task.BaseCommit, taskLaneRework(req.Task))
}

func bindPrivateProcess05Tier(run *marshal.Run, p plan.ExecutionPlan) {
	if p.Mode == plan.ModeUltra {
		run.Tier = marshal.Ultra
		run.Process05Parallel = true
	} else {
		run.Tier = marshal.Standard
		run.Process05Parallel = false
	}
}

// process05RiskAssessmentID names a provider turn's risk assessment for its
// canonical task. An assessment binds its worktree, and every task lane
// attempt (a rework included) has its own, so a lane turn's assessment is also
// named by its lane.
func process05RiskAssessmentID(provider, canonicalTaskID string, task execution.TaskExecution) risk.AssessmentID {
	id := "process05-" + provider + "-" + canonicalTaskID
	if strings.HasPrefix(task.RunID, "lane-") {
		id += "-" + task.RunID
	}
	return risk.AssessmentID(id)
}
