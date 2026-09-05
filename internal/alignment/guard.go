package alignment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Guard is the central Core #2 Alignment Guard.
type Guard struct {
	diffInspector *DiffInspector
	blastAuditor  *BlastRadiusAuditor
}

func NewGuard() *Guard {
	return &Guard{
		diffInspector: NewDiffInspector(),
		blastAuditor:  NewBlastRadiusAuditor(),
	}
}

// EvaluateChanges checks proposed code changes against Goal contract invariants.
func (g *Guard) EvaluateChanges(
	ctx context.Context,
	goal model.GoalContract,
	taskGoalRevision int64,
	predictedFiles []string,
	observedFiles []string,
	deletedFiles []string,
	patchContent string,
	hasApprovalEvidence bool,
) (Result, error) {
	result := Result{
		Passed:          true,
		Violations:      make([]Violation, 0),
		PredictedFiles:  predictedFiles,
		ObservedFiles:   observedFiles,
		PredictedRadius: len(predictedFiles),
		ObservedRadius:  len(observedFiles),
		EvaluatedAt:     time.Now().UTC(),
	}

	// 1. Goal Version Drift Check
	if taskGoalRevision > 0 && taskGoalRevision < goal.Revision {
		result.Violations = append(result.Violations, Violation{
			Type:             CheckGoalDrift,
			Severity:         "BLOCKING",
			Message:          fmt.Sprintf("task executed against Goal revision %d; active Goal is revision %d", taskGoalRevision, goal.Revision),
			RequiresApproval: true,
		})
	}

	// 2. Blast Radius and Scope Lock Check
	blastViolations, err := g.blastAuditor.AuditBlastRadius(goal, predictedFiles, observedFiles, 3)
	if err != nil {
		return result, err
	}
	result.Violations = append(result.Violations, blastViolations...)

	// 3. Deletion-as-Satisfaction and Validation Removal Check
	diffViolations, err := g.diffInspector.InspectPatch(goal, deletedFiles, patchContent, hasApprovalEvidence)
	if err != nil {
		return result, err
	}
	result.Violations = append(result.Violations, diffViolations...)

	// Determine overall pass/fail status
	for _, v := range result.Violations {
		if v.Severity == "BLOCKING" {
			result.Passed = false
			break
		}
	}

	return result, nil
}

// ValidateMergeReadiness asserts that changes can safely reach merge-ready state.
func (g *Guard) ValidateMergeReadiness(
	ctx context.Context,
	goal model.GoalContract,
	taskGoalRevision int64,
	predictedFiles []string,
	observedFiles []string,
	deletedFiles []string,
	patchContent string,
	hasApprovalEvidence bool,
) error {
	res, err := g.EvaluateChanges(ctx, goal, taskGoalRevision, predictedFiles, observedFiles, deletedFiles, patchContent, hasApprovalEvidence)
	if err != nil {
		return err
	}

	if res.Passed {
		return nil
	}

	// Return the most specific blocking error
	for _, v := range res.Violations {
		switch v.Type {
		case CheckBlastRadius:
			return fmt.Errorf("%w: %s", ErrBlastRadiusExceeded, v.Message)
		case CheckDeletionAsSatisfaction:
			return fmt.Errorf("%w: %s", ErrDeletionAsSatisfaction, v.Message)
		case CheckValidationRemoval:
			return fmt.Errorf("%w: %s", ErrValidationRemoval, v.Message)
		case CheckScopeLock:
			return fmt.Errorf("%w: %s", ErrScopeViolation, v.Message)
		case CheckGoalDrift:
			return fmt.Errorf("%w: %s", ErrGoalDrift, v.Message)
		}
	}

	return ErrAlignmentViolation
}

// EvaluateScopeExpansionClaim enforces that claims justifying scope expansion as "necessary"
// remain UNSUPPORTED unless an explicit operator/user decision exists.
func (g *Guard) EvaluateScopeExpansionClaim(claim model.Claim, hasOperatorDecision bool) (model.Claim, error) {
	lowerText := strings.ToLower(claim.NormalizedText)
	lowerSubj := strings.ToLower(claim.Subject)

	isScopeExpansionClaim := strings.Contains(lowerText, "necessary") &&
		(strings.Contains(lowerText, "scope") || strings.Contains(lowerText, "expansion") || strings.Contains(lowerSubj, "scope"))

	if isScopeExpansionClaim && !hasOperatorDecision {
		updated := claim
		updated.State = model.ClaimStateUnsupported
		updated.StateReason = "Scope expansion justified with 'necessary' remains UNSUPPORTED pending explicit operator/user approval"
		updated.EvaluatedAt = time.Now().UTC()
		return updated, nil
	}

	return claim, nil
}
