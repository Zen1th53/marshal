package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/trustscore"
)

type EvidenceTrustScoreService struct {
	evaluator *trustscore.Evaluator
}

func NewEvidenceTrustScoreService(evaluator *trustscore.Evaluator) *EvidenceTrustScoreService {
	return &EvidenceTrustScoreService{evaluator: evaluator}
}

func (s *EvidenceTrustScoreService) ScoreChange(ctx context.Context, changeDigest string) (*trustscore.Result, error) {
	if s == nil || s.evaluator == nil {
		return nil, fmt.Errorf("trust score service uninitialized")
	}
	return s.evaluator.ComputeScore(ctx, changeDigest, []trustscore.Component{
		{Name: "quorum", Score: 95.0, Weight: 1.0},
	})
}
