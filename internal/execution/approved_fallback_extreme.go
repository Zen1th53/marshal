//go:build !governed

package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"reflect"
	"regexp"
	"slices"
)

var approvedFallbackModelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:-]{0,127}$`)

func (e *Engine) approvedFallbackSelector(ctx context.Context, run ExecutionRun, task TaskExecution, from, next string) (string, error) {
	refuse := func() (string, error) {
		return "", fmt.Errorf("%w: no exact owner-reviewed producer fallback", ErrRunConflict)
	}
	if e.planReader == nil {
		return refuse()
	}
	if e.goalReader == nil {
		return refuse()
	}
	target := run.SessionID
	if target == "" {
		target = run.GoalID
	}
	goal, err := e.goalReader.GetActiveGoalContract(ctx, target)
	if err != nil || goal.ID != run.GoalID || goal.Revision != run.GoalRevision || goal.ProjectID != string(run.ProjectID) || !goal.Confirmation.Settled() || goal.Confirmation == model.ConfirmationCancelled {
		return refuse()
	}
	p, err := e.planReader.GetActivePlan(ctx, run.ProjectID)
	if err != nil {
		return "", err
	}
	if p.ID != run.PlanID || p.Version != run.PlanVersion || p.ProjectID != run.ProjectID || p.State != plan.StateApproved || p.Goal.GoalID != run.GoalID || p.Goal.Revision != run.GoalRevision || p.ApprovedScope == nil || p.ApprovalScopeDigest != p.ApprovedScope.Digest() {
		return refuse()
	}
	if p.ApprovedScope.Goal != p.Goal || !reflect.DeepEqual(p.ApprovedScope.Budget, p.Budget) || p.ApprovedScope.Mode != p.Mode {
		return refuse()
	}
	var original, planned *plan.Task
	for i := range p.ApprovedScope.Tasks {
		if p.ApprovedScope.Tasks[i].ID == task.TaskID {
			if original != nil {
				return refuse()
			}
			original = &p.ApprovedScope.Tasks[i]
		}
	}
	for i := range p.Tasks {
		if p.Tasks[i].ID == task.TaskID {
			if planned != nil {
				return refuse()
			}
			planned = &p.Tasks[i]
		}
	}
	if original == nil || planned == nil || !reflect.DeepEqual(*original, *planned) || !slices.Equal(planned.DependsOn, task.Dependencies) || !slices.Equal(planned.Paths, task.TargetFiles) || planned.Mutating != task.Mutates || planned.Title != task.Description || !slices.Equal(p.Checks[task.TaskID], p.ApprovedScope.Checks[task.TaskID]) || !reflect.DeepEqual(p.RequiredProjectContract, p.ApprovedScope.RequiredProjectContract) {
		return refuse()
	}
	// Extreme never stops for a missing plan-sealed fallback: the run's goal,
	// plan and task scope are verified above, so the next provider continues
	// with the model of its current verified harness profile, which the
	// runtime supplies after checking that profile.
	sealed, err := approvedSealedFallback(p, task, from, next)
	if err == nil {
		return sealed, nil
	}
	if modelName, ok := verifiedFallbackModelFrom(ctx, next); ok {
		if task.AssignedHarness == next && task.AssignedModel != "" && task.AssignedModel != modelName {
			return refuse()
		}
		return modelName, nil
	}
	return "", err
}

// approvedSealedFallback is the owner-reviewed producer fallback sealed in the
// approved plan for exactly this task and provider pair.
func approvedSealedFallback(p plan.ExecutionPlan, task TaskExecution, from, next string) (string, error) {
	refuse := func() (string, error) {
		return "", fmt.Errorf("%w: no exact owner-reviewed producer fallback", ErrRunConflict)
	}
	seal := p.ApprovedScope.ProducerAssignments
	if seal == nil {
		return refuse()
	}
	if seal.ReviewedPlanVersion < 1 || seal.ReviewedPlanVersion+1 != p.Version {
		return refuse()
	}
	var current []plan.Assignment
	for _, a := range p.Assignments.Assignments {
		if a.Role == plan.RoleDeveloper && !a.Independent {
			current = append(current, a)
		}
	}
	aRaw, err := json.Marshal(current)
	if err != nil {
		return refuse()
	}
	bRaw, err := json.Marshal(seal.Assignments)
	if err != nil || string(aRaw) != string(bRaw) {
		return refuse()
	}
	matches := 0
	modelName := ""
	for _, a := range seal.Assignments {
		if a.Role != plan.RoleDeveloper || a.Independent || !slices.Contains(a.Tasks, task.TaskID) {
			continue
		}
		matches++
		if executionHarnessName(a.Provider) != executionHarnessName(from) || executionHarnessName(a.Harness) != executionHarnessName(from) || a.Fallback == nil || executionHarnessName(a.Fallback.Harness) != executionHarnessName(next) || executionHarnessName(a.Fallback.Provider) != executionHarnessName(next) || !approvedFallbackModelName.MatchString(a.Fallback.Model) || a.Fallback.HarnessVersion == "" || a.Fallback.ProbeEvidenceID == "" || a.Fallback.ProbedAt == nil || a.Fallback.ProbedAt.IsZero() || a.Fallback.ExpiresAt == nil || a.Fallback.ExpiresAt.IsZero() {
			return refuse()
		}
		if task.AssignedHarness == from && task.AssignedModel != a.Model || task.AssignedHarness == next && task.AssignedModel != a.Fallback.Model {
			return refuse()
		}
		modelName = a.Fallback.Model
	}
	if matches != 1 {
		return refuse()
	}
	return modelName, nil
}

type verifiedFallbackModelKey struct{}

type verifiedFallbackModel struct{ provider, model string }

// WithVerifiedFallbackModel carries the model of the next provider's current
// verified harness profile, checked by the runtime, for a failover or switch
// whose plan holds no sealed fallback for that provider.
func WithVerifiedFallbackModel(ctx context.Context, provider, modelName string) context.Context {
	return context.WithValue(ctx, verifiedFallbackModelKey{}, verifiedFallbackModel{provider: provider, model: modelName})
}

func verifiedFallbackModelFrom(ctx context.Context, next string) (string, bool) {
	v, ok := ctx.Value(verifiedFallbackModelKey{}).(verifiedFallbackModel)
	if !ok || executionHarnessName(v.provider) != executionHarnessName(next) || !approvedFallbackModelName.MatchString(v.model) {
		return "", false
	}
	return v.model, true
}
