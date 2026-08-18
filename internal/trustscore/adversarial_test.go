package trustscore

import (
	"context"
	"errors"
	"testing"
)

func TestT54A04AdversarialBoundaries(t *testing.T) {
	ev := NewEvaluator()
	ctx := context.Background()

	// Stale change digest
	_, err := ev.ComputeScore(ctx, "STALE", nil)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("expected ErrStale, got %v", err)
	}
}
