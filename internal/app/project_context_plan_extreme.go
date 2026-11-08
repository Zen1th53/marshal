//go:build !governed

package app

import (
	"context"
	"errors"
	"reflect"

	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func (s *PlanService) bindRequiredPlanProjectContract(ctx context.Context, p *plan.ExecutionPlan) error {
	accepted, err := s.runtime.store.ReadAcceptedProjectContract(ctx, s.runtime.ProjectContractBinding())
	if errors.Is(err, projectcontext.ErrReviewPending) {
		// An uninitialized project may retain a planning draft, which conveys
		// no execution authority. Actual required-context effects refuse nil.
		return nil
	}
	if err != nil {
		return err
	}
	if string(p.ProjectID) != s.runtime.ProjectIdentity() {
		return projectcontext.Changed("project_context_plan_extreme.go:25")
	}
	ref := projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}
	p.RequiredProjectContract = &ref
	return nil
}

func requiredApprovedPlanProjectContract(p plan.ExecutionPlan) (projectcontext.ContractRef, error) {
	if p.RequiredProjectContract == nil {
		return projectcontext.ContractRef{}, projectcontext.ErrReviewPending
	}
	if p.ApprovedScope == nil || !reflect.DeepEqual(p.RequiredProjectContract, p.ApprovedScope.RequiredProjectContract) || p.ApprovalScopeDigest != p.ApprovedScope.Digest() {
		return projectcontext.ContractRef{}, projectcontext.Changed("project_context_plan_extreme.go:37")
	}
	return *p.RequiredProjectContract, nil
}
