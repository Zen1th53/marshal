package tournament

import (
	"context"
	"testing"
)

func TestArenaEvaluateTournament(t *testing.T) {
	ar := NewArena()
	ctx := context.Background()

	cands := []CandidateRun{{ID: "cand-1", AgentID: "agent-a"}}
	dims := []Dimension{{Name: "speed", Weight: 1.0}}

	res, err := ar.EvaluateTournament(ctx, cands, dims)
	if err != nil {
		t.Fatalf("EvaluateTournament: %v", err)
	}
	if res.WinnerID != "cand-1" {
		t.Fatalf("expected cand-1, got %s", res.WinnerID)
	}
}
