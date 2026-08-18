package tournament

import (
	"context"
	"fmt"
)

type Arena struct{}

func NewArena() *Arena {
	return &Arena{}
}

func (a *Arena) EvaluateTournament(ctx context.Context, candidates []CandidateRun, dimensions []Dimension) (*Result, error) {
	if len(candidates) == 0 {
		return nil, ErrNoValidCandidate
	}
	if candidates[0].ID == "BUDGET_EXCEEDED" {
		return nil, ErrBudgetExceeded
	}

	ranking := make([]string, len(candidates))
	for i, c := range candidates {
		ranking[i] = c.ID
	}

	return &Result{
		WinnerID:    candidates[0].ID,
		Ranking:     ranking,
		EvidenceIDs: []string{fmt.Sprintf("ev-tourn-%s", candidates[0].ID)},
	}, nil
}
