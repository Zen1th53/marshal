package epistemic

import (
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// ContradictionDiscipline enforces Discipline D: Contradiction Discipline.
// It detects deterministic conflicts, preserves counter-evidence, identifies circular provenance,
// and marks claims as CONTESTED.
type ContradictionDiscipline struct{}

func NewContradictionDiscipline() *ContradictionDiscipline {
	return &ContradictionDiscipline{}
}

// DetectContradiction checks if a claim has conflicting deterministic evidence or conflicting peer claims.
func (d *ContradictionDiscipline) DetectContradiction(claim model.Claim, newEvidence model.EvidenceRef) (bool, string) {
	// If newEvidence contradicts existing verified or supported claim
	if newEvidence.IsDeterministic {
		if claim.State == model.ClaimStateVerified {
			// Check if new evidence reports failure or contradicts claim
			if newEvidence.Metadata != nil {
				if exitCode, ok := newEvidence.Metadata["exit_code"]; ok && exitCode != "0" {
					return true, fmt.Sprintf("deterministic test failed with exit code %s, contradicting verified claim", exitCode)
				}
				if result, ok := newEvidence.Metadata["result"]; ok && (result == "fail" || result == "error") {
					return true, fmt.Sprintf("deterministic test result %q contradicts verified claim", result)
				}
			}
		}
	}
	return false, ""
}

// DetectCrossClaimConflict checks if two claims in the same scope make contradictory assertions.
func (d *ContradictionDiscipline) DetectCrossClaimConflict(a, b model.Claim) (bool, string) {
	if a.ID == b.ID || a.Scope != b.Scope {
		return false, ""
	}

	normSubjA := strings.ToLower(strings.TrimSpace(a.Subject))
	normSubjB := strings.ToLower(strings.TrimSpace(b.Subject))

	normTextA := strings.ToLower(strings.TrimSpace(a.NormalizedText))
	normTextB := strings.ToLower(strings.TrimSpace(b.NormalizedText))

	// If subjects match or address the same property, but one asserts success/true and the other asserts failure/false
	if normSubjA == normSubjB && normTextA != normTextB {
		if (strings.Contains(normTextA, "pass") && strings.Contains(normTextB, "fail")) ||
			(strings.Contains(normTextA, "enabled") && strings.Contains(normTextB, "disabled")) ||
			(strings.Contains(normTextA, "secure") && strings.Contains(normTextB, "vulnerable")) ||
			(a.State == model.ClaimStateVerified && b.State == model.ClaimStateInvalidated) {
			return true, fmt.Sprintf("cross-claim contradiction between %s and %s in scope %s: %q vs %q",
				a.ID, b.ID, a.Scope, a.NormalizedText, b.NormalizedText)
		}
	}

	return false, ""
}

// DetectCircularProvenance checks whether claims or source clusters form a circular dependency loop.
func (d *ContradictionDiscipline) DetectCircularProvenance(claim model.Claim, allClaims map[string]model.Claim) error {
	visited := make(map[string]bool)
	curr := claim.ID

	for curr != "" {
		if visited[curr] {
			return fmt.Errorf("%w: cycle involving claim %s", ErrCircularProvenance, curr)
		}
		visited[curr] = true

		parent, ok := allClaims[curr]
		if !ok {
			break
		}
		curr = parent.PredecessorID
	}

	return nil
}

// ApplyContradiction marks a claim as CONTESTED, preserving the counter-evidence.
func (d *ContradictionDiscipline) ApplyContradiction(claim model.Claim, counterEv model.EvidenceRef, reason string) (model.Claim, model.ClaimTransition) {
	now := time.Now().UTC()
	updated := claim
	updated.State = model.ClaimStateContested
	updated.StateReason = reason
	updated.ContradictingEvidence = append(updated.ContradictingEvidence, counterEv)
	updated.EvaluatedAt = now
	updated.UpdatedAt = now

	trans := model.ClaimTransition{
		TransitionID: fmt.Sprintf("TRANS-CONTEST-%s-%d", claim.ID, now.UnixNano()),
		ClaimID:      claim.ID,
		FromState:    claim.State,
		ToState:      model.ClaimStateContested,
		Reason:       reason,
		Actor: model.AuthorProvenance{
			AgentID: "epistemic-contradiction-discipline",
			Harness: "marshal-core",
		},
		EvidenceRef: &counterEv,
		Timestamp:   now,
	}

	return updated, trans
}
