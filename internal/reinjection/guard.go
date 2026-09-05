package reinjection

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
)

// ConstraintGuard enforces anti-weakening and anti-tampering invariants for Goal constraints.
type ConstraintGuard struct{}

func NewConstraintGuard() *ConstraintGuard {
	return &ConstraintGuard{}
}

var exceptionPatterns = []string{
	"waive constraint",
	"bypass constraint",
	"ignore constraint",
	"carve out exception",
	"exception necessary for constraint",
	"relax constraint",
	"disable constraint",
}

// ValidateHandoff verifies that a handoff faithfully preserves the active Goal's
// hard constraints and matches the canonical digest.
func (g *ConstraintGuard) ValidateHandoff(activeGoal model.GoalContract, handoff protocol.Handoff) error {
	// 1. Detect agent-generated "necessary" exceptions in handoff prose
	for _, claimVal := range handoff.Claims {
		lower := strings.ToLower(claimVal)
		for _, p := range exceptionPatterns {
			if strings.Contains(lower, p) {
				return fmt.Errorf("%w: agent prose contains forbidden exception: %q",
					ErrConstraintWeakeningForbidden, p)
			}
		}
	}
	for _, unres := range handoff.Unresolved {
		lower := strings.ToLower(unres)
		for _, p := range exceptionPatterns {
			if strings.Contains(lower, p) {
				return fmt.Errorf("%w: agent unresolved contains forbidden exception: %q",
					ErrConstraintWeakeningForbidden, p)
			}
		}
	}

	// 2. Compute canonical active digest
	activeDigest := ComputeConstraintsDigest(activeGoal.Constraints, activeGoal.DoNotDo)

	// If handoff has no digest, check if any hard constraints exist in active goal
	if handoff.ConstraintsDigest == "" {
		for _, c := range activeGoal.Constraints {
			if c.IsHard {
				return fmt.Errorf("%w: handoff omitted constraints digest for active hard constraint %s",
					ErrMissingHardConstraint, c.ID)
			}
		}
		return nil
	}

	// If digest matches perfectly, handoff is intact
	if handoff.ConstraintsDigest == activeDigest {
		return nil
	}

	// 3. If digest doesn't match, determine why:
	// A) Stale Goal Revision check
	for _, ref := range handoff.ConstraintRefs {
		if ref.Revision < activeGoal.Revision {
			return fmt.Errorf("%w: handoff constraint %s references Goal revision %d, active revision is %d",
				ErrStaleGoalRevision, ref.ID, ref.Revision, activeGoal.Revision)
		}
	}

	// B) Check if any hard constraint was deleted or weakened
	refMap := make(map[string]protocol.ConstraintRef, len(handoff.ConstraintRefs))
	for _, ref := range handoff.ConstraintRefs {
		refMap[ref.ID] = ref
	}

	for _, c := range activeGoal.Constraints {
		if c.IsHard {
			ref, exists := refMap[c.ID]
			if !exists {
				return fmt.Errorf("%w: active hard constraint %s was deleted from handoff",
					ErrConstraintWeakeningForbidden, c.ID)
			}
			expectedDigest := ComputeSingleConstraintDigest(c)
			if ref.Digest != expectedDigest {
				return fmt.Errorf("%w: hard constraint %s was modified in handoff (digest mismatch)",
					ErrConstraintWeakeningForbidden, c.ID)
			}
		}
	}

	return fmt.Errorf("%w: handoff digest %s != active digest %s",
		ErrConstraintTampered, handoff.ConstraintsDigest, activeDigest)
}

// ValidateGoalRevisionTransition ensures an agent cannot delete or weaken operator constraints
// when proposing a Goal revision.
func (g *ConstraintGuard) ValidateGoalRevisionTransition(
	oldGoal, newGoal model.GoalContract,
	actor model.AuthorProvenance,
	authoritySource string,
) error {
	return model.CanModifyGoal(authoritySource, oldGoal, newGoal)
}
