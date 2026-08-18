package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/evolution"
)

func TestEvolutionLabServiceAdapter(t *testing.T) {
	lab := evolution.NewLab()
	ctx := context.Background()
	svc := NewEvolutionLabService(lab)

	res, err := svc.RunExperiment(ctx, 5)
	if err != nil {
		t.Fatalf("RunExperiment failed: %v", err)
	}
	if res.BestIndividual.ID != "ind-best-1" {
		t.Fatalf("expected ind-best-1, got %s", res.BestIndividual.ID)
	}
}
