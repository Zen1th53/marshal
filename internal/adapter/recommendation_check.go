package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/recommendation"
)

type SelfImprovementService struct {
	engine *recommendation.Engine
}

func NewSelfImprovementService(engine *recommendation.Engine) *SelfImprovementService {
	return &SelfImprovementService{engine: engine}
}

func (s *SelfImprovementService) SuggestOptimization(ctx context.Context, query string) (*recommendation.Recommendation, error) {
	if s == nil || s.engine == nil {
		return nil, fmt.Errorf("self improvement service uninitialized")
	}
	return s.engine.Generate(ctx, query)
}
