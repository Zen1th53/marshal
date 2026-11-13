//go:build !governed

package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/risk"
)

func (r *Runtime) selectProcess05TaskLane(ctx context.Context, s *MarshalService, run marshal.Run, req driver.Request, parent *execution.ExecutionRun) (*execution.ExecutionRun, error) {
	if serialProcess05Base(s.Gate, run) {
		return parent, nil
	}
	if !run.Process05Bound || req.Task.Mode != marshal.Governed || !marshalDepsMerged(run, req.Task) {
		return nil, fmt.Errorf("%w: task lane requires reviewed dependencies", execution.ErrRunBlocked)
	}
	if parent.State == execution.RunPaused {
		// The person paused the run: no new lane opens until they resume.
		return nil, ErrProcess05Paused
	}
	if err := r.Execution().Engine().SetTaskLaneParent(ctx, parent.RunID, run.BaseCommit); err != nil {
		return nil, err
	}
	return r.Execution().Engine().OpenTaskLaneRework(ctx, parent.RunID, req.Task.PlanTaskID, req.Task.BaseCommit, taskLaneRework(req.Task))
}

// continuePriorTaskLanes numbers a newly bound run's task attempts after the
// attempts an earlier run of the same approved plan already made. Process 05
// keeps one execution parent per approved plan version and goal revision
// (StartRunBound), so a second Marshal run of that plan (/marshal use-plan)
// shares its task lanes. Its first attempt of a task is the next attempt after
// the parent's newest one: it never reopens an earlier run's lane, and lane
// admission keeps its order. Returns and the rework limit still count only
// this run's returns.
func continuePriorTaskLanes(ctx context.Context, s *MarshalService, run *marshal.Run, goal model.GoalContract, projectID projectid.ID) error {
	if s.runtime == nil || !run.Process05Bound || !run.Process05Parallel {
		return nil
	}
	service := s.runtime.Execution()
	if service == nil {
		return nil
	}
	runs, err := service.ListRuns(ctx)
	if err != nil {
		return err
	}
	for _, parent := range runs {
		if parent.ParentRunID != "" || len(parent.TaskLanes) == 0 || parent.SessionID != goal.SessionID || parent.ProjectID != projectID || parent.GoalID != goal.ID || parent.GoalRevision != goal.Revision || parent.PlanID != run.PlanID || parent.PlanVersion != run.PlanVersion {
			continue
		}
		for i := range run.Tasks {
			id := run.Tasks[i].PlanTaskID
			lane := parent.TaskLanes[id]
			if lane == "" {
				continue
			}
			attempt, ok := execution.TaskLaneAttempt(parent.RunID, id, lane)
			if !ok {
				return fmt.Errorf("%w: task %s has a lane outside its approved attempts", execution.ErrRunConflict, id)
			}
			run.Tasks[i].LaneReworkBase = max(run.Tasks[i].LaneReworkBase, attempt+1)
		}
	}
	return nil
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
