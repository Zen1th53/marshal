//go:build !governed

package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

// Source inputs are not authority: task choices must resolve to the runtime's
// current verified harness record. Approval continues through Process 04.
func (s *PlanService) applyModelPlanInputs(ctx context.Context, built *plan.ExecutionPlan, req CreatePlanRequest, now time.Time) error {
	if len(req.Checks) == 0 && len(req.TaskProviders) == 0 {
		return nil
	}
	known := make(map[string]plan.Task, len(built.Tasks))
	for _, task := range built.Tasks {
		known[task.ID] = task
	}
	for id, commands := range req.Checks {
		if _, ok := known[id]; !ok || len(commands) == 0 {
			return fmt.Errorf("%w: checks reference unknown or empty task %s", model.ErrInvalid, id)
		}
		for _, command := range commands {
			if strings.TrimSpace(command) == "" {
				return fmt.Errorf("%w: empty check for %s", model.ErrInvalid, id)
			}
		}
	}
	if len(req.Checks) > 0 {
		built.Checks = make(map[string][]string, len(req.Checks))
		for _, task := range built.Tasks {
			if len(req.Checks[task.ID]) == 0 {
				return fmt.Errorf("%w: missing check for %s", model.ErrInvalid, task.ID)
			}
			if len(task.Criteria) != 1 {
				return fmt.Errorf("%w: task %s needs one exact criterion for its executable checks", model.ErrInvalid, task.ID)
			}
			built.Checks[task.ID] = append([]string(nil), req.Checks[task.ID]...)
		}
	}
	for id := range req.TaskProviders {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("%w: provider references unknown task %s", model.ErrInvalid, id)
		}
	}
	if len(req.TaskProviders) == 0 {
		return nil
	}
	assignments := built.Assignments.Assignments[:0]
	for _, assignment := range built.Assignments.Assignments {
		if assignment.Independent {
			assignments = append(assignments, assignment)
			continue
		}
		remaining := make([]string, 0, len(assignment.Tasks))
		for _, id := range assignment.Tasks {
			if req.TaskProviders[id] == "" {
				remaining = append(remaining, id)
			}
		}
		if len(remaining) > 0 || len(assignment.Tasks) == 0 {
			assignment.Tasks = remaining
			assignments = append(assignments, assignment)
		}
	}
	built.Assignments.Assignments = assignments
	for _, task := range built.Tasks {
		provider := normalizeProvider(req.TaskProviders[task.ID])
		if provider == "" {
			continue
		}
		if provider != "codex" && provider != "claude" && provider != "opencode" && !(provider == "agy" && privateAGYEnabled()) {
			return fmt.Errorf("%w: unsupported governed provider %s", model.ErrInvalid, provider)
		}
		profile, err := s.runtime.store.GetHarnessProfile(ctx, marshalHarnessName(provider))
		if err != nil || profile == nil {
			return fmt.Errorf("%w: verified harness profile required for %s", model.ErrInvalid, provider)
		}
		installed := installedCLIVersion(ctx, provider)
		role := plan.Role(task.Role)
		if role == "" {
			role = plan.RoleDeveloper
		}
		capacity := goalintake.UnknownCapacity(provider, true)
		candidate := plan.HarnessCandidate{Profile: *profile, InstalledVersion: installed, Provider: provider, Capacity: capacity}
		selected := plan.AssignHarnesses(plan.AssignRequest{Team: plan.Team{Members: []plan.Member{{Role: role}}}, Tasks: []plan.Task{task}, Assessment: built.Assessment, Candidates: []plan.HarnessCandidate{candidate}, RequiredCapabilities: req.RequiredCapabilities, EstimatedContextTokens: req.EstimatedContextTokens, Now: now})
		if !selected.Complete() || len(selected.Assignments) != 1 {
			return fmt.Errorf("%w: current governed harness evidence does not admit %s for %s", model.ErrInvalid, provider, task.ID)
		}
		assignment := selected.Assignments[0]
		assignment.Tasks = []string{task.ID}
		built.Assignments.Assignments = append(built.Assignments.Assignments, assignment)
		built.Routes[task.ID] = plan.Route{Provider: provider, Model: assignment.Model, Governance: assignment.Governance, Reason: "Exact model-proposed task provider validated against runtime harness evidence"}
	}
	return nil
}

func privatePlanTaskWorker(p plan.ExecutionPlan, task plan.Task) (string, bool, error) {
	worker := ""
	for _, assignment := range p.Assignments.Assignments {
		if assignment.Independent || assignment.Harness == "" {
			continue
		}
		if task.Role != "" && string(assignment.Role) != task.Role {
			continue
		}
		if !containsMarshal(assignment.Tasks, task.ID) {
			continue
		}
		candidate := normalizeProvider(assignment.Harness)
		if worker != "" && worker != candidate {
			return "", true, fmt.Errorf("approved task %s has ambiguous governed harnesses", task.ID)
		}
		worker = candidate
	}
	if worker != "" {
		return worker, true, nil
	}
	// Older role-only/single-worker plans still need executable alias
	// normalization. Resolve them here so independent checking assignments
	// cannot fall through into the Community producer fallback.
	for _, assignment := range p.Assignments.Assignments {
		if assignment.Independent || assignment.Harness == "" || len(assignment.Tasks) != 0 {
			continue
		}
		if len(p.Assignments.Assignments) != 1 && assignment.Role != plan.Role(task.Role) {
			continue
		}
		candidate := normalizeProvider(assignment.Harness)
		if worker != "" && worker != candidate {
			return "", true, fmt.Errorf("approved task %s has ambiguous governed harnesses", task.ID)
		}
		worker = candidate
	}
	if worker == "" {
		return "", true, fmt.Errorf("approved task %s has no governed producer harness", task.ID)
	}
	return worker, true, nil
}
