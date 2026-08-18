package evolution

import (
	"context"
	"testing"
)

func TestLabStart(t *testing.T) {
	lab := NewLab()
	ctx := context.Background()

	res, err := lab.Start(ctx, LabConfig{Population: 10, Generations: 5, MaxParallel: 2})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.BestIndividual.ID != "ind-best-1" {
		t.Fatalf("expected ind-best-1, got %s", res.BestIndividual.ID)
	}
}
