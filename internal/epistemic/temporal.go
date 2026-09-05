package epistemic

import (
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// TemporalValidityDiscipline enforces Discipline E: Temporal Validity.
// It invalidates claims fine-grained when relevant code or files change,
// without cascading stale status to unrelated claims.
type TemporalValidityDiscipline struct{}

func NewTemporalValidityDiscipline() *TemporalValidityDiscipline {
	return &TemporalValidityDiscipline{}
}

// InvalidateOnCodeChange checks which claims depend on changed files and transitions ONLY those to STALE.
// Unrelated claims remain in their current state.
func (d *TemporalValidityDiscipline) InvalidateOnCodeChange(
	claims []model.Claim,
	changedFiles []string,
	newCommitSHA string,
) ([]model.Claim, []model.ClaimTransition) {
	if len(changedFiles) == 0 {
		return claims, nil
	}

	changedMap := make(map[string]bool, len(changedFiles))
	for _, f := range changedFiles {
		changedMap[f] = true
	}

	now := time.Now().UTC()
	updatedClaims := make([]model.Claim, len(claims))
	transitions := make([]model.ClaimTransition, 0)

	for i, c := range claims {
		updatedClaims[i] = c

		// Only claims currently VERIFIED or SUPPORTED can become STALE
		if c.State != model.ClaimStateVerified && c.State != model.ClaimStateSupported {
			continue
		}

		// Check if any bound file intersects with changed files
		fileModified := false
		var touchedFile string
		for _, boundFile := range c.Binding.Files {
			if changedMap[boundFile] {
				fileModified = true
				touchedFile = boundFile
				break
			}
		}

		if fileModified {
			reason := fmt.Sprintf("Bound file %s was modified in commit %s; formerly verified evidence is now stale",
				touchedFile, newCommitSHA)

			trans := model.ClaimTransition{
				TransitionID: fmt.Sprintf("TRANS-STALE-%s-%d", c.ID, now.UnixNano()),
				ClaimID:      c.ID,
				FromState:    c.State,
				ToState:      model.ClaimStateStale,
				Reason:       reason,
				Actor: model.AuthorProvenance{
					AgentID: "epistemic-temporal-discipline",
					Harness: "marshal-core",
				},
				Timestamp: now,
			}
			transitions = append(transitions, trans)

			updatedClaims[i].State = model.ClaimStateStale
			updatedClaims[i].StateReason = reason
			updatedClaims[i].EvaluatedAt = now
			updatedClaims[i].UpdatedAt = now
		}
	}

	return updatedClaims, transitions
}
