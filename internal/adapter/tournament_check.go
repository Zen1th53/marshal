package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/tournament"
)

type AgentTournamentService struct {
	arena *tournament.Arena
}

func NewAgentTournamentService(arena *tournament.Arena) *AgentTournamentService {
	return &AgentTournamentService{arena: arena}
}

func (s *AgentTournamentService) RunTournament(ctx context.Context, candidateID string) (*tournament.Result, error) {
	if s == nil || s.arena == nil {
		return nil, fmt.Errorf("tournament service uninitialized")
	}
	return s.arena.EvaluateTournament(ctx, []tournament.CandidateRun{
		{ID: candidateID, TaskID: "t-1", AgentID: "agent-1"},
	}, nil)
}
