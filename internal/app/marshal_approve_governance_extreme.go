//go:build !governed

package app

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func (*MarshalService) requiresCanonicalGovernedApproval() bool { return true }

func (s *MarshalService) canonicalProjectContractCurrent(ctx context.Context, p plan.ExecutionPlan) error {
	if s == nil || s.runtime == nil || p.RequiredProjectContract == nil {
		return projectcontext.ErrReviewPending
	}
	accepted, err := s.Store.ReadAcceptedProjectContract(ctx, s.runtime.ProjectContractBinding())
	if err != nil {
		return err
	}
	ref := projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}
	if ref != *p.RequiredProjectContract {
		return fmt.Errorf("%w: approved project contract changed", projectcontext.Changed("marshal_approve_governance_extreme.go:25"))
	}
	return nil
}
