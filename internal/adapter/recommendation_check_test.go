package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/recommendation"
)

func TestSelfImprovementServiceAdapter(t *testing.T) {
	eng := recommendation.NewEngine()
	ctx := context.Background()
	svc := NewSelfImprovementService(eng)

	rec, err := svc.SuggestOptimization(ctx, "tune context budget")
	if err != nil {
		t.Fatalf("SuggestOptimization failed: %v", err)
	}
	if rec.ID == "" {
		t.Fatalf("expected non-empty recommendation ID")
	}
}
