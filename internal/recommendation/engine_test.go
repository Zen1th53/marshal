package recommendation

import (
	"context"
	"errors"
	"testing"
)

func TestEngineGenerateReturnsHonestNoData(t *testing.T) {
	eng := NewEngine()
	ctx := context.Background()

	// Wave 1 requirement: eliminate fabricated 0.94 / scheduler.max_concurrent outputs.
	// Must return honest ErrNoData.
	rec, err := eng.Generate(ctx, "optimize scheduler")
	if rec != nil {
		t.Fatalf("expected nil recommendation, got %+v", rec)
	}
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData, got %v", err)
	}
}

func TestEngineApplyLifecycle(t *testing.T) {
	eng := NewEngine()
	ctx := context.Background()

	// Apply non-existent returns ErrNotFound
	err := eng.Apply(ctx, "rec-1", "admin-1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing recommendation, got %v", err)
	}

	// Stored recommendation can be applied with approval
	eng.recs["rec-1"] = &Recommendation{
		ID:     "rec-1",
		Kind:   "CONFIG_OPTIMIZATION",
		Status: "PROPOSED",
	}

	if err := eng.Apply(ctx, "rec-1", "admin-1"); err != nil {
		t.Fatalf("unexpected error applying with approval: %v", err)
	}

	if eng.recs["rec-1"].Status != "APPLIED" {
		t.Fatalf("expected status APPLIED, got %s", eng.recs["rec-1"].Status)
	}
}
