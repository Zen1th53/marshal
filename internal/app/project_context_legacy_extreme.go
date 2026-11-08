//go:build !governed

package app

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/projectid"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// Imported-task Run has no canonical approved-plan/owned-engine snapshot, so
// it cannot mint required context authority. The actual supported plan path
// dispatches through guarded Process05 callbacks. Contract data or a local role
// alone cannot turn this legacy entry point into a qualified provider call.
func (*Runtime) requiredLegacyRunAdmission(context.Context, RunRequest) error {
	return fmt.Errorf("%w: start the reviewed approved plan to retain mandatory project context", projectcontext.ErrReviewPending)
}

// All real Runtime-composed services install this private admission callback.
// Standalone store-only services remain data/test compositions and obtain no
// runtime ownership or required-context qualification from this callback.
func (r *Runtime) requiredMarshalDispatchAdmission(ctx context.Context, run marshal.Run, task marshal.Task) error {
	if !run.Process05Bound || task.Mode != marshal.Governed {
		return fmt.Errorf("%w: this worker route cannot retain mandatory project context; use the reviewed approved plan", projectcontext.ErrReviewPending)
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return err
	}
	p, err := r.store.GetActivePlan(ctx, projectid.ID(r.ProjectIdentity()))
	if err != nil {
		return err
	}
	ref, err := requiredApprovedPlanProjectContract(p)
	if err != nil {
		return err
	}
	if p.ID != run.PlanID || p.Version != run.PlanVersion {
		return projectcontext.Changed("project_context_legacy_extreme.go:42")
	}
	b := projectcontext.Binding{PlanID: p.ID, PlanVersion: p.Version, TaskID: task.PlanTaskID, CanonicalTaskID: canonicalPlanTaskID(p.ID, p.Version, task.PlanTaskID)}
	selection, err := requiredProjectPlanSelection(p, b, accepted.Contract)
	if err != nil {
		return err
	}
	return r.store.ValidateProjectContractScope(ctx, r.ProjectContractBinding(), ref, selection)
}
