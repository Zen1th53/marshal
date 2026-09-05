package interpretation

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// InvalidationResult encapsulates the outcome of processing a user clarification/correction.
type InvalidationResult struct {
	UpdatedGoal         model.GoalContract `json:"updated_goal"`
	InvalidatedClaimIDs []string           `json:"invalidated_claim_ids"`
	RollbackCheckpoint  string             `json:"rollback_checkpoint,omitempty"`
	Message             string             `json:"message"`
}

// Invalidator applies user feedback to supersede a Goal revision and invalidate dependent artifacts.
type Invalidator struct {
	store *store.Store
}

func NewInvalidator(st *store.Store) *Invalidator {
	return &Invalidator{store: st}
}

// ApplyCorrection creates Goal revision v(N+1), records resolution, and invalidates affected claims.
func (inv *Invalidator) ApplyCorrection(
	ctx context.Context,
	currentGoal model.GoalContract,
	answeredDecisionID string,
	chosenOption string,
	actor model.AuthorProvenance,
) (InvalidationResult, error) {
	now := time.Now().UTC()

	// 1. Create incremented goal revision
	newGoal := currentGoal
	newGoal.Revision = currentGoal.Revision + 1
	newGoal.UpdatedAt = now
	newGoal.UnderstandingState = model.GoalReady

	// Filter out the resolved decision
	var remainingDecisions []model.UnresolvedDecision
	var resolvedQuestion string
	for _, dec := range currentGoal.UnresolvedDecisions {
		if dec.ID == answeredDecisionID {
			resolvedQuestion = dec.Question
		} else {
			remainingDecisions = append(remainingDecisions, dec)
		}
	}
	newGoal.UnresolvedDecisions = remainingDecisions

	// Add an assumption or constraint recording the user's explicit decision
	if chosenOption != "" {
		newGoal.Constraints = append(newGoal.Constraints, model.Constraint{
			ID:     fmt.Sprintf("user-decision-%d", now.UnixNano()),
			Text:   fmt.Sprintf("Operator decision on %q: %s", resolvedQuestion, chosenOption),
			Source: "operator",
			IsHard: true,
			Scope:  "global",
		})
	}

	// 2. Persist new goal contract revision if store is available
	if inv.store != nil {
		if err := inv.store.SaveGoalContract(ctx, newGoal, currentGoal.Revision); err != nil {
			return InvalidationResult{}, fmt.Errorf("save updated goal v%d: %w", newGoal.Revision, err)
		}
	}

	// 3. Find and invalidate claims formed under the previous revision that relied on contested assumptions
	var invalidatedClaimIDs []string
	if inv.store != nil {
		priorClaims, err := inv.store.ListClaimsByGoal(ctx, currentGoal.ID, currentGoal.Revision)
		if err == nil {
			for _, cl := range priorClaims {
				if cl.Criticality.IsCritical() {
					// Mark critical claims from the contested revision as INVALIDATED with reason
					transition := model.ClaimTransition{
						TransitionID: fmt.Sprintf("tr-inv-%d-%s", now.UnixNano(), cl.ID),
						ClaimID:      cl.ID,
						FromState:    cl.State,
						ToState:      model.ClaimStateInvalidated,
						Reason:       fmt.Sprintf("Superseded by user correction on goal revision v%d: %s", newGoal.Revision, chosenOption),
						Actor:        actor,
						Timestamp:    now,
					}
					cl.State = model.ClaimStateInvalidated
					cl.StateReason = transition.Reason
					_ = inv.store.SaveClaim(ctx, cl)
					_ = inv.store.RecordClaimTransition(ctx, transition)
					invalidatedClaimIDs = append(invalidatedClaimIDs, cl.ID)
				}
			}
		}
	}

	return InvalidationResult{
		UpdatedGoal:         newGoal,
		InvalidatedClaimIDs: invalidatedClaimIDs,
		Message:             fmt.Sprintf("Goal v%d successfully established from user correction; %d claims invalidated", newGoal.Revision, len(invalidatedClaimIDs)),
	}, nil
}
