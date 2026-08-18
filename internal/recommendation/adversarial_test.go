package recommendation

import (
	"context"
	"errors"
	"testing"
)

func TestT47A04AdversarialBoundaries(t *testing.T) {
	eng := NewEngine()
	ctx := context.Background()

	// Apply without approver
	err := eng.Apply(ctx, "rec-1", "")
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
}
