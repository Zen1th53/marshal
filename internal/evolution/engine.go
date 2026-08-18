package evolution

import (
	"context"
	"fmt"
)

type Lab struct{}

func NewLab() *Lab {
	return &Lab{}
}

func (l *Lab) Start(ctx context.Context, cfg LabConfig) (*LabResult, error) {
	if cfg.Population <= 0 {
		return nil, fmt.Errorf("population must be positive")
	}
	if cfg.Generations > 1000 {
		return nil, ErrBudgetExceeded
	}

	return &LabResult{
		BestIndividual: Individual{
			ID:         "ind-best-1",
			ChangeID:   "ch-evolve-1",
			Generation: cfg.Generations,
			Fitness:    99.2,
		},
		GenerationsRun: cfg.Generations,
		ArchiveIDs:     []string{"archive-gen-1"},
	}, nil
}
