package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// BindApprovedPlan starts Marshal orchestration from the exact current
// Process 04 plan. The initial bridge handles one task; this restriction keeps
// Process 05 from executing sibling tasks behind Marshal's scheduler.
func (s *MarshalService) BindApprovedPlan(ctx context.Context, runID string) (marshal.Run, error) {
	if err := s.ready(); err != nil {
		return marshal.Run{}, err
	}
	if !marshalIdentifier(runID) {
		return marshal.Run{}, errors.New("invalid Marshal run ID")
	}
	p, err := s.Store.GetActivePlan(ctx, projectid.ID(s.ProjectID))
	if err != nil {
		return marshal.Run{}, err
	}
	if p.State != plan.StateApproved || len(p.Tasks) != 1 {
		return marshal.Run{}, errors.New("Process 05 binding requires a current approved one-task plan")
	}
	goal, err := s.Store.GetGoalContract(ctx, p.Goal.GoalID, p.Goal.Revision)
	if err != nil {
		return marshal.Run{}, err
	}
	active, err := s.Store.GetActiveGoalContract(ctx, goal.SessionID)
	if err != nil || active.ID != goal.ID || active.Revision != goal.Revision || !active.Confirmation.Settled() {
		return marshal.Run{}, errors.New("Process 05 binding requires the current confirmed goal")
	}
	handoff, err := plan.PrepareHandoff(p, goal, projectid.ID(s.ProjectID), s.clock())
	if err != nil {
		return marshal.Run{}, err
	}
	if err := handoff.Validate(); err != nil {
		return marshal.Run{}, err
	}
	if p.Mode == plan.ModeUltra && (s.Gate == nil || !s.Gate.Capability(marshal.CapabilityMarshal)) {
		return marshal.Run{}, errors.New("ULTRA plan requires a current Marshal entitlement")
	}
	pt := p.Tasks[0]
	if !marshalIdentifier(pt.ID) || len(pt.Criteria) == 0 || len(pt.Paths) == 0 || len(p.Checks[pt.ID]) == 0 {
		return marshal.Run{}, errors.New("approved task lacks Marshal scope or executable checks")
	}
	worker := ""
	for _, assignment := range p.Assignments.Assignments {
		if assignment.Harness == "" {
			continue
		}
		if len(p.Assignments.Assignments) == 1 || assignment.Role == plan.Role(pt.Role) || containsMarshal(assignment.Tasks, pt.ID) {
			if worker != "" && worker != assignment.Harness {
				return marshal.Run{}, errors.New("approved task has ambiguous governed harnesses")
			}
			worker = assignment.Harness
		}
	}
	if worker == "" || s.GovernedDrivers[worker] == nil {
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
	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		return marshal.Run{}, err
	}
	base, err := gitMarshal(ctx, s.Repository, "rev-parse", "HEAD")
	if err != nil {
		return marshal.Run{}, err
	}
	task := marshal.Task{PlanTaskID: pt.ID, Worker: worker, Mode: marshal.Governed, State: marshal.Queued, BaseCommit: base, Branch: "marshal/" + runID + "/" + pt.ID, Files: append([]string(nil), pt.Paths...), Criteria: append([]string(nil), pt.Criteria...), DependsOn: append([]string(nil), pt.DependsOn...)}
	for _, command := range p.Checks[pt.ID] {
		if strings.TrimSpace(command) == "" {
			return marshal.Run{}, errors.New("approved plan contains an empty check")
		}
		task.Checks = append(task.Checks, marshal.Check{Command: command, Criteria: append([]string(nil), pt.Criteria...)})
	}
	run := marshal.Run{PlanID: p.ID, PlanVersion: p.Version, BaseCommit: base, GoalBinding: goal.OriginalRequest, Tasks: []marshal.Task{task}, State: marshal.Approved, Settings: settings.Value}
	run.ApprovalScopeDigest = marshalApprovalDigest(p.ApprovalScopeDigest, run.Budget)
	if err := s.save(ctx, runID, run, 0); err != nil {
		return marshal.Run{}, err
	}
	return run, s.record(ctx, runID, "", events.EventTypeMarshalPlanApproved, map[string]any{"plan_version": p.Version, "source": "process04"})
}
