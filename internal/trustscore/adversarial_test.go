package trustscore

import (
	"context"
	"errors"
	"testing"
)

func TestT54A04AdversarialBoundaries(t *testing.T) {
	ev := NewEvaluator()
	ctx := context.Background()

	// Empty change digest
	_, err := ev.ComputeScore(ctx, "", []Component{{Name: "test", Score: 80.0}})
	if err == nil {
		t.Fatal("expected error for empty changeDigest, got nil")
	}

	// Empty components returns ErrNoData rather than fabricated 92.5 score
	_, err = ev.ComputeScore(ctx, "sha256:digest", nil)
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData for empty components, got %v", err)
	}

	// Zero/negative weight handling does not divide by zero
	res, err := ev.ComputeScore(ctx, "sha256:digest", []Component{{Name: "zero", Score: 80.0, Weight: 0.0}})
	if err != nil {
		t.Fatalf("unexpected error with zero weight: %v", err)
	}
	if res.Overall != 80.0 {
		t.Fatalf("expected score 80.0, got %f", res.Overall)
	}
}
