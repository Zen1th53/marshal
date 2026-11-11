package tui

// Control bindings: preparation, reading the exact canonical target.
// Split mechanically from controlbind.go; see that file's header for the
// binding contract.

import (
	"context"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

func (s *ControlSource) prepareGoal(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	goal, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("the active goal could not be read: %w", err)
	}
	if goal.ID == "" {
		return Target{}, errors.New("no goal is active in this session")
	}
	if goal.Confirmation == model.ConfirmationCancelled {
		return Target{}, fmt.Errorf("goal %s is already cancelled", goal.ID)
	}
	return Target{
		Kind:     "goal",
		ID:       goal.ID,
		Revision: goal.Revision,
		// The request digest binds the goal to the exact text it came from, so
		// a goal that was revised shows a different digest.
		Digest:  goal.RequestDigest,
		Scope:   fmt.Sprintf("goal %s in session %s", goal.ID, s.SessionID),
		Summary: fmt.Sprintf("goal %s revision %d (%s)", goal.ID, goal.Revision, goal.Confirmation),
	}, nil
}

// approvalTarget binds the exact action, target, scope, digest and revision the
// governance contract requires an approval to carry.
func approvalTarget(a *execution.RuntimeApproval) Target {
	return Target{
		Kind:     "approval",
		ID:       a.ApprovalID,
		Revision: a.PlanVersion,
		// The action digest is what makes this approval about one exact
		// operation. Approving a digest that has since changed is the TOCTOU
		// failure the contract names.
		Digest: approvalDigest(a),
		Scope:  a.Scope,
		Summary: fmt.Sprintf("%s on %s (risk %s, plan %s v%d)",
			a.OperationType, a.TargetResource, a.RiskLevel, a.PlanID, a.PlanVersion),
	}
}

func approvalDigest(a *execution.RuntimeApproval) string {
	if a == nil {
		return ""
	}
	return a.ActionDigest + "/" + a.StateDigest
}
