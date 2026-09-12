package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// ApproveGoal records Process 03 confirmation as a new CAS-guarded Goal
// revision. Surfaces call this boundary rather than writing goal rows.
func (r *Runtime) ApproveGoal(ctx context.Context, sessionID string, expectedRevision int64) (model.GoalContract, error) {
	if r == nil || r.store == nil {
		return model.GoalContract{}, fmt.Errorf("%w: goal service is unavailable", model.ErrUnavailable)
	}
	goal, err := r.store.GetActiveGoalContract(ctx, sessionID)
	if err != nil {
		return model.GoalContract{}, err
	}
	if goal.Revision != expectedRevision {
		return model.GoalContract{}, fmt.Errorf("%w: goal moved from revision %d to %d",
			model.ErrGoalConflict, expectedRevision, goal.Revision)
	}
	approved, err := goalintake.ApproveContract(goal)
	if err != nil {
		return model.GoalContract{}, err
	}
	approved.Revision = expectedRevision + 1
	approved.RevisionReason = "operator confirmed goal"
	approved.UpdatedAt = time.Now().UTC()
	if err := r.store.SaveGoalContract(ctx, approved, expectedRevision); err != nil {
		return model.GoalContract{}, err
	}
	return r.store.GetActiveGoalContract(ctx, sessionID)
}

// ReviseGoal applies Process 03's canonical revision rule to an exact durable
// GoalContract revision.  The caller provides only the new interpretation and
// its reason; the original request and every hard constraint are reconstructed
// from canonical state and cannot be weakened by the surface.
func (r *Runtime) ReviseGoal(ctx context.Context, sessionID string, expectedRevision int64, interpretation, reason string) (model.GoalContract, error) {
	if r == nil || r.store == nil {
		return model.GoalContract{}, fmt.Errorf("%w: goal service is unavailable", model.ErrUnavailable)
	}
	if strings.TrimSpace(reason) == "" {
		return model.GoalContract{}, fmt.Errorf("%w: revision reason is required", model.ErrGoalInvalid)
	}
	goal, err := r.store.GetActiveGoalContract(ctx, sessionID)
	if err != nil {
		return model.GoalContract{}, err
	}
	if goal.Revision != expectedRevision {
		return model.GoalContract{}, fmt.Errorf("%w: goal moved from revision %d to %d", model.ErrGoalConflict, expectedRevision, goal.Revision)
	}
	version, err := constitution.ParseVersion(goal.ConstitutionVersion)
	if err != nil {
		return model.GoalContract{}, fmt.Errorf("parse goal constitution: %w", err)
	}
	intake := goalintake.Intake{
		OriginalRequest: goal.OriginalRequest,
		Interpretation:  goal.DesiredOutcome,
		ProjectID:       projectid.ID(goal.ProjectID), SessionID: goal.SessionID, Version: version,
		Constraints:        append([]model.Constraint(nil), goal.Constraints...),
		Assumptions:        append([]model.Assumption(nil), goal.Assumptions...),
		Ambiguities:        append([]model.UnresolvedDecision(nil), goal.UnresolvedDecisions...),
		AcceptanceCriteria: append([]string(nil), goal.SuccessCriteria...),
		Confirmation:       goalintake.ConfirmationState(goal.Confirmation),
		RequestDigest:      goal.RequestDigest,
	}
	revised, err := goalintake.Revise(intake, interpretation, reason)
	if err != nil {
		return model.GoalContract{}, err
	}
	if dropped, ok := goalintake.HardConstraintsPreserved(intake, revised); !ok {
		return model.GoalContract{}, fmt.Errorf("%w: revision removed hard constraints: %s", model.ErrGoalHardConstraint, strings.Join(dropped, ", "))
	}
	next := goal
	next.DesiredOutcome = revised.Interpretation
	next.Confirmation = model.ConfirmationPending
	next.Revision = goal.Revision + 1
	next.RevisionReason = reason
	next.UpdatedAt = time.Now().UTC()
	if err := r.store.SaveGoalContract(ctx, next, goal.Revision); err != nil {
		return model.GoalContract{}, err
	}
	return r.store.GetActiveGoalContract(ctx, sessionID)
}
