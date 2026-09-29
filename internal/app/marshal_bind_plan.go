package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// canonicalPlanProjectID is the Process 03–05 project identity. Marshal's
// local run records can still use the legacy project row in the same store.
func (s *MarshalService) CanonicalPlanProjectID() projectid.ID {
	if binding, found := projectid.LoadBinding(filepath.Join(s.Repository, projectid.StateDirName)); found {
		return binding.ID
	}
	return projectid.ID(s.ProjectID)
}

// BindApprovedPlan starts Marshal orchestration from the exact current
// Process 04 plan. Each task remains separate so Marshal reviews and merges
// the result before admitting the next Process 05 task.
func (s *MarshalService) BindApprovedPlan(ctx context.Context, runID string) (marshal.Run, error) {
	if err := s.ready(); err != nil {
		return marshal.Run{}, err
	}
	if !marshalIdentifier(runID) {
		return marshal.Run{}, errors.New("invalid Marshal run ID")
	}
	planProjectID := s.CanonicalPlanProjectID()
	p, err := s.Store.GetActivePlan(ctx, planProjectID)
	if err != nil {
		return marshal.Run{}, err
	}
	if p.State != plan.StateApproved || len(p.Tasks) == 0 {
		return marshal.Run{}, errors.New("Process 05 binding requires a current approved plan with tasks")
	}
	goal, err := s.Store.GetGoalContract(ctx, p.Goal.GoalID, p.Goal.Revision)
	if err != nil {
		return marshal.Run{}, err
	}
	active, err := s.Store.GetActiveGoalContract(ctx, goal.SessionID)
	if err != nil || active.ID != goal.ID || active.Revision != goal.Revision || !active.Confirmation.Settled() {
		return marshal.Run{}, errors.New("Process 05 binding requires the current confirmed goal")
	}
	handoff, err := plan.PrepareHandoff(p, goal, planProjectID, s.clock())
	if err != nil {
		return marshal.Run{}, err
	}
	if err := handoff.Validate(); err != nil {
		return marshal.Run{}, err
	}
	if p.Mode == plan.ModeUltra && (s.Gate == nil || !s.Gate.Capability(marshal.CapabilityMarshal)) {
		return marshal.Run{}, errors.New("ULTRA plan requires a current Marshal entitlement")
	}
	ordered, err := marshalOrderedPlanTasks(p.Tasks)
	if err != nil {
		return marshal.Run{}, err
	}
	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		return marshal.Run{}, err
	}
	base, err := gitMarshal(ctx, s.Repository, "rev-parse", "HEAD")
	if err != nil {
		return marshal.Run{}, err
	}
	tasks := make([]marshal.Task, 0, len(ordered))
	for _, pt := range ordered {
		if !marshalIdentifier(pt.ID) || len(pt.Criteria) == 0 || len(pt.Paths) == 0 || len(p.Checks[pt.ID]) == 0 {
			return marshal.Run{}, fmt.Errorf("approved task %s lacks Marshal scope or executable checks", pt.ID)
		}
		worker, err := marshalPlanTaskWorker(p, pt)
		if err != nil {
			return marshal.Run{}, err
		}
		if s.GovernedDrivers[worker] == nil {
			return marshal.Run{}, fmt.Errorf("Process 05 harness %q is not wired into Marshal", worker)
		}
		workerProvider := worker
		switch worker {
		case "claude-code":
			workerProvider = "claude"
		case "antigravity":
			workerProvider = "agy"
		}
		if s.ModelProvider != "" && s.ModelProvider == workerProvider {
			return marshal.Run{}, fmt.Errorf("Marshal model %s would review its own Process 05 work; choose another with /marshal model", s.ModelProvider)
		}
		task := marshal.Task{PlanTaskID: pt.ID, Title: pt.Title, Worker: worker, Mode: marshal.Governed, State: marshal.Queued, BaseCommit: base, Branch: "marshal/" + runID + "/" + pt.ID, Files: append([]string(nil), pt.Paths...), Criteria: append([]string(nil), pt.Criteria...), DependsOn: append([]string(nil), pt.DependsOn...), Instructions: pt.Instructions, ExpectedOutput: pt.ExpectedOutput}
		for _, command := range p.Checks[pt.ID] {
			if strings.TrimSpace(command) == "" {
				return marshal.Run{}, errors.New("approved plan contains an empty check")
			}
			task.Checks = append(task.Checks, marshal.Check{Command: command, Criteria: append([]string(nil), pt.Criteria...)})
		}
		tasks = append(tasks, task)
	}
	if err := instructionsPresent(settings.Value, tasks); err != nil {
		return marshal.Run{}, err
	}
	run := marshal.Run{PlanID: p.ID, Process05Bound: true, PlanVersion: p.Version, BaseCommit: base, GoalBinding: goal.OriginalRequest, Tasks: tasks, State: marshal.Approved, Settings: settings.Value}
	run.ApprovalScopeDigest = marshalApprovalDigest(p.ApprovalScopeDigest, run.Budget, run.Settings.EffectiveControl())
	if err := s.save(ctx, runID, run, 0); err != nil {
		return marshal.Run{}, err
	}
	return run, s.record(ctx, runID, "", events.EventTypeMarshalPlanApproved, map[string]any{"plan_version": p.Version, "source": "process04"})
}

func marshalPlanTaskWorker(p plan.ExecutionPlan, task plan.Task) (string, error) {
	worker := ""
	for _, assignment := range p.Assignments.Assignments {
		if assignment.Harness == "" {
			continue
		}
		if len(p.Assignments.Assignments) == 1 || containsMarshal(assignment.Tasks, task.ID) || (len(assignment.Tasks) == 0 && assignment.Role == plan.Role(task.Role)) {
			if worker != "" && worker != assignment.Harness {
				return "", fmt.Errorf("approved task %s has ambiguous governed harnesses", task.ID)
			}
			worker = assignment.Harness
		}
	}
	if worker == "" {
		return "", fmt.Errorf("approved task %s has no governed harness", task.ID)
	}
	return worker, nil
}

func marshalOrderedPlanTasks(tasks []plan.Task) ([]plan.Task, error) {
	remaining := make(map[string]plan.Task, len(tasks))
	for _, task := range tasks {
		if _, exists := remaining[task.ID]; exists {
			return nil, fmt.Errorf("duplicate approved task %s", task.ID)
		}
		remaining[task.ID] = task
	}
	ordered := make([]plan.Task, 0, len(tasks))
	done := make(map[string]bool, len(tasks))
	for len(ordered) < len(tasks) {
		progressed := false
		for _, task := range tasks {
			if done[task.ID] {
				continue
			}
			ready := true
			for _, dep := range task.DependsOn {
				if _, exists := remaining[dep]; !exists {
					return nil, fmt.Errorf("approved task %s has unknown dependency %s", task.ID, dep)
				}
				ready = ready && done[dep]
			}
			if ready {
				ordered = append(ordered, task)
				done[task.ID] = true
				progressed = true
			}
		}
		if !progressed {
			return nil, errors.New("approved task graph has a cycle")
		}
	}
	return ordered, nil
}
