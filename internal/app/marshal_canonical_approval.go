package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
)

func (s *MarshalService) validateCanonicalGovernedDraft(ctx context.Context, run marshal.Run) error {
	if s == nil || s.runtime == nil || run.Process05Bound || run.State != marshal.Drafting {
		return errors.New("runtime-owned Process 04 draft required")
	}
	p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return err
	}
	active, err := s.Store.GetActivePlan(ctx, s.CanonicalPlanProjectID())
	if err != nil || active.ID != p.ID || active.Version != p.Version {
		return errors.New("Process 04 plan is no longer active")
	}
	if p.State != plan.StateReady || p.ProjectID != s.CanonicalPlanProjectID() || len(planExecutionBlockers(p)) != 0 {
		return errors.New("Process 04 plan is not ready with verified routes, assignments, team and checks")
	}
	goal, err := s.Store.GetGoalContract(ctx, p.Goal.GoalID, p.Goal.Revision)
	if err != nil {
		return err
	}
	current, err := s.Store.GetActiveGoalContract(ctx, goal.SessionID)
	if err != nil || !goal.Confirmation.Settled() || !p.Goal.Matches(current) {
		return errors.New("Process 04 Goal is no longer current and confirmed")
	}
	if err := s.canonicalProjectContractCurrent(ctx, p); err != nil {
		return err
	}
	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil || !reflect.DeepEqual(settings.Value, run.Settings) || run.Budget != run.Settings.Budget {
		return errors.New("Marshal settings or budget changed since plan review")
	}
	if p.Mode == plan.ModeUltra && (s.Gate == nil || !s.Gate.Capability(marshal.CapabilityMarshal)) {
		return errors.New("ULTRA plan requires a current Marshal entitlement")
	}
	if len(p.Tasks) != len(run.Tasks) {
		return errors.New("Process 04 task count differs from Marshal draft")
	}
	head, err := gitMarshal(ctx, s.Repository, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head) != run.BaseCommit {
		return errors.New("repository changed since Marshal draft")
	}
	for _, pt := range p.Tasks {
		i := taskIndex(run, pt.ID)
		if i < 0 || len(pt.Criteria) != 1 || len(p.Checks[pt.ID]) == 0 {
			return fmt.Errorf("Process 04 task %s has no exact check mapping", pt.ID)
		}
		worker, err := marshalPlanTaskWorker(p, pt)
		if err != nil {
			return err
		}
		mt := run.Tasks[i]
		if mt.Mode != marshal.Governed || mt.Worker != worker || s.GovernedDrivers[worker] == nil || mt.Title != pt.Title || mt.Instructions != pt.Instructions || mt.ExpectedOutput != pt.ExpectedOutput || string(mt.Type) != pt.Type || !slices.Equal(mt.Files, pt.Paths) || !slices.Equal(mt.Criteria, pt.Criteria) || !slices.Equal(mt.DependsOn, pt.DependsOn) || len(mt.Checks) != len(p.Checks[pt.ID]) {
			return fmt.Errorf("Process 04 task %s differs from reviewed Marshal draft", pt.ID)
		}
		for i, check := range mt.Checks {
			if check.Command != p.Checks[pt.ID][i] || !slices.Equal(check.Criteria, pt.Criteria) {
				return fmt.Errorf("Process 04 check %s differs from reviewed Marshal draft", pt.ID)
			}
		}
		if s.InstalledVersion == nil || s.workerGovernance(ctx, worker).State != constitution.GovernanceVerified {
			return fmt.Errorf("current verified harness profile required for %s", worker)
		}
		profile, err := s.Store.GetHarnessProfile(ctx, marshalHarnessName(worker))
		if err != nil || profile == nil || profile.ProbeEvidenceID == "" {
			return fmt.Errorf("current verified harness profile required for %s", worker)
		}
		matched := false
		for _, a := range p.Assignments.Assignments {
			if a.Independent || a.Harness != marshalHarnessName(worker) && a.Harness != worker || !planAssignmentCoversTask(a, pt) {
				continue
			}
			if a.ProbedAt.Equal(profile.ProbedAt) && a.ProbeEvidenceID != "" && a.ProbeEvidenceID == profile.ProbeEvidenceID && a.HarnessVersion == s.InstalledVersion(ctx, worker) && a.Provider == p.Routes[pt.ID].Provider && a.Model == p.Routes[pt.ID].Model && a.Model == profile.DefaultModel {
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("governed producer evidence changed for %s", pt.ID)
		}
	}
	return nil
}

// canonicalBoundRun reconstructs Process 05 inputs from the approved plan,
// rather than promoting model-authored draft task objects into authority.
func (s *MarshalService) canonicalBoundRun(ctx context.Context, runID string, p plan.ExecutionPlan, old marshal.Run) (marshal.Run, error) {
	goal, err := s.Store.GetGoalContract(ctx, p.Goal.GoalID, p.Goal.Revision)
	if err != nil {
		return marshal.Run{}, err
	}
	handoff, err := plan.PrepareHandoff(p, goal, s.CanonicalPlanProjectID(), s.clock())
	if err != nil {
		return marshal.Run{}, err
	}
	if err := handoff.Validate(); err != nil {
		return marshal.Run{}, err
	}
	ordered, err := marshalOrderedPlanTasks(p.Tasks)
	if err != nil {
		return marshal.Run{}, err
	}
	run := marshal.Run{Compatibility: old.Compatibility, ArtifactRevision: old.ArtifactRevision, PlanID: p.ID, Process05Bound: true, PlanVersion: old.PlanVersion + 1,
		BaseCommit: old.BaseCommit, GoalBinding: goal.OriginalRequest,
		State: marshal.Approved, Settings: old.Settings, Budget: old.Budget, Pack: old.Pack}
	bindPrivateProcess05Tier(&run, p)
	for _, pt := range ordered {
		worker, err := marshalPlanTaskWorker(p, pt)
		if err != nil {
			return marshal.Run{}, err
		}
		kind := marshal.TaskType(pt.Type)
		if kind == "" {
			kind = marshal.TaskChange
			if !pt.Mutating {
				kind = marshal.TaskInspection
			}
		}
		task := marshal.Task{Type: kind, PlanTaskID: pt.ID, Title: pt.Title, Worker: worker,
			Mode: marshal.Governed, State: marshal.Queued, BaseCommit: old.BaseCommit,
			Branch: marshalTaskBranch(runID, pt.ID, old.ArtifactRevision),
			Files:  append([]string(nil), pt.Paths...), Criteria: append([]string(nil), pt.Criteria...),
			DependsOn: append([]string(nil), pt.DependsOn...), Instructions: pt.Instructions, ExpectedOutput: pt.ExpectedOutput}
		for _, command := range p.Checks[pt.ID] {
			task.Checks = append(task.Checks, marshal.Check{Command: command, Criteria: append([]string(nil), pt.Criteria...)})
		}
		if i := taskIndex(old, pt.ID); i >= 0 {
			task.WorkerSelection = old.Tasks[i].WorkerSelection
		}
		run.Tasks = append(run.Tasks, task)
	}
	if err := instructionsPresent(run.Settings, run.Tasks); err != nil {
		return marshal.Run{}, err
	}
	return run, nil
}
