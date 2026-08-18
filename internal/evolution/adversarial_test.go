package evolution

import (
	"context"
	"errors"
	"testing"
)

func TestT18A04AdversarialBoundaries(t *testing.T) {
	lab := NewLab()
	ctx := context.Background()

	// Budget exceeded
	_, err := lab.Start(ctx, LabConfig{Population: 10, Generations: 9999})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
}
