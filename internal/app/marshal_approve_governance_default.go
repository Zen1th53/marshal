//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
)

// Community Marshal retains its interactive draft approval flow. The
// canonical Process 04/05 admission requirement belongs to Extreme, where
// governed workers consume the project contract and execution plan.
func (s *MarshalService) requiresCanonicalGovernedApproval() bool {
	return false
}

func (s *MarshalService) canonicalProjectContractCurrent(context.Context, plan.ExecutionPlan) error {
	return nil
}

func (s *MarshalService) canonicalizeHeadlessDraft(_ context.Context, _ string, draft MarshalDraft, _ marshal.Budget) (MarshalDraft, error) {
	return draft, nil
}
