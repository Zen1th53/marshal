package budget

import (
	"context"
	"errors"
	"testing"
)

func TestT12A04AdversarialBoundaries(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	// Mandatory section overflow
	_, err := mgr.Allocate(ctx,
		Budget{MaxTokens: 500, ReserveTokens: 100},
		[]SectionPriority{
			{Kind: "system", MinTokens: 600, Mandatory: true},
		},
	)
	if !errors.Is(err, ErrMandatoryOverflow) {
		t.Fatalf("expected ErrMandatoryOverflow, got %v", err)
	}
}
