package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/tournament"
)

func TestAgentTournamentServiceAdapter(t *testing.T) {
	ar := tournament.NewArena()
	ctx := context.Background()
	svc := NewAgentTournamentService(ar)

	res, err := svc.RunTournament(ctx, "cand-1")
	if err != nil {
		t.Fatalf("RunTournament failed: %v", err)
	}
	if res.WinnerID != "cand-1" {
		t.Fatalf("expected cand-1, got %s", res.WinnerID)
	}
}
