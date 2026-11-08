//go:build !governed

package app

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"slices"
)

func (r *Runtime) validateApprovedFallbackProfile(ctx context.Context, s *MarshalService, run execution.ExecutionRun, task execution.TaskExecution, next string) error {
	refuse := func() error {
		return fmt.Errorf("%w: approved fallback profile or contract changed", execution.ErrRunConflict)
	}
	p, err := r.store.GetActivePlan(ctx, run.ProjectID)
	if err != nil {
		return err
	}
	if p.ID != run.PlanID || p.Version != run.PlanVersion || p.Goal.GoalID != run.GoalID || p.Goal.Revision != run.GoalRevision || p.State != plan.StateApproved || p.ApprovedScope == nil {
		return refuse()
	}
	if _, err = approvedExecutionTaskInstructions(p, run, task, "", requiredProviderEnvelopeBytes); err != nil {
		return err
	}
	ref, err := requiredApprovedPlanProjectContract(p)
	if err != nil {
		return err
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return err
	}
	if task.CanonicalTaskID != canonicalPlanTaskID(p.ID, p.Version, task.TaskID) {
		return refuse()
	}
	binding := projectcontext.Binding{PlanID: p.ID, PlanVersion: p.Version, TaskID: task.TaskID, CanonicalTaskID: task.CanonicalTaskID}
	selection, err := requiredProjectPlanSelection(p, binding, accepted.Contract)
	if err != nil {
		return err
	}
	// Metadata-only scoped compatibility conveys no body/ACL/invocation grant.
	// The actual owned callback still requires complete current context.
	if err = r.store.ValidateProjectContractScope(ctx, r.ProjectContractBinding(), ref, selection); err != nil {
		return err
	}
	var fallback *plan.Fallback
	count := 0
	var sealed []plan.Assignment
	if p.ApprovedScope.ProducerAssignments != nil {
		sealed = p.ApprovedScope.ProducerAssignments.Assignments
	}
	for _, a := range sealed {
		if a.Role == plan.RoleDeveloper && !a.Independent && slices.Contains(a.Tasks, task.TaskID) {
			fallback = a.Fallback
			count++
		}
	}
	if count != 1 || fallback == nil || marshalHarnessName(normalizeProvider(fallback.Harness)) != marshalHarnessName(next) || fallback.Model == "" || fallback.ProbedAt == nil || fallback.ExpiresAt == nil {
		// No sealed fallback for this provider: Extreme continues with the
		// next provider's current verified harness profile instead.
		if _, err := r.verifiedFallbackProfileModel(ctx, s, next); err != nil {
			return refuse()
		}
		return nil
	}
	profile, err := s.Store.GetHarnessProfile(ctx, marshalHarnessName(next))
	if err != nil || profile == nil {
		return refuse()
	}
	now := s.clock()
	if profile.Harness != marshalHarnessName(next) || profile.Validate() != nil || profile.ProbedAt.After(now) || profile.ExpiresAt.IsZero() || !profile.IsFresh(now) || profile.InstalledVersion != fallback.HarnessVersion || profile.ProbeEvidenceID != fallback.ProbeEvidenceID || !profile.ProbedAt.Equal(*fallback.ProbedAt) || !profile.ExpiresAt.Equal(*fallback.ExpiresAt) || !slices.Contains(profile.SupportedModels, fallback.Model) {
		return refuse()
	}
	if s.InstalledVersion == nil || s.InstalledVersion(ctx, next) != fallback.HarnessVersion {
		return refuse()
	}
	return nil
}

// verifiedFallbackProfileModel is the model of provider's current verified
// harness profile: probed, fresh, valid, of the installed CLI version and
// naming its default model among its supported models.
func (r *Runtime) verifiedFallbackProfileModel(ctx context.Context, s *MarshalService, provider string) (string, error) {
	refuse := fmt.Errorf("%w: %s has no current verified harness profile", execution.ErrRunConflict, provider)
	profile, err := s.Store.GetHarnessProfile(ctx, marshalHarnessName(provider))
	if err != nil || profile == nil {
		return "", refuse
	}
	now := s.clock()
	if profile.Harness != marshalHarnessName(provider) || profile.Validate() != nil || profile.ProbeEvidenceID == "" || profile.ProbedAt.After(now) || profile.ExpiresAt.IsZero() || !profile.IsFresh(now) || profile.DefaultModel == "" || !slices.Contains(profile.SupportedModels, profile.DefaultModel) {
		return "", refuse
	}
	if s.InstalledVersion != nil && s.InstalledVersion(ctx, provider) != profile.InstalledVersion {
		return "", refuse
	}
	if err := validatePrivateAGYModel(normalizeProvider(provider), profile.DefaultModel); err != nil {
		return "", err
	}
	return profile.DefaultModel, nil
}
