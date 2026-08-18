package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/evolution"
)

type EvolutionLabService struct {
	lab *evolution.Lab
}

func NewEvolutionLabService(lab *evolution.Lab) *EvolutionLabService {
	return &EvolutionLabService{lab: lab}
}

func (s *EvolutionLabService) RunExperiment(ctx context.Context, generations int) (*evolution.LabResult, error) {
	if s == nil || s.lab == nil {
		return nil, fmt.Errorf("evolution lab service uninitialized")
	}
	return s.lab.Start(ctx, evolution.LabConfig{Population: 10, Generations: generations, MaxParallel: 2})
}
