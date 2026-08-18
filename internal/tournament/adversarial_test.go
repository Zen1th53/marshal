package tournament

import (
	"context"
	"errors"
	"testing"
)

func TestT17A04AdversarialBoundaries(t *testing.T) {
	ar := NewArena()
	ctx := context.Background()

	// Budget exceeded
	_, err := ar.EvaluateTournament(ctx, []CandidateRun{{ID: "BUDGET_EXCEEDED"}}, nil)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
}
