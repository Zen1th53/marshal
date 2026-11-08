package recommendation

import (
	"context"
	"errors"
	"testing"
)

func TestT47A04AdversarialBoundaries(t *testing.T) {
	eng := NewEngine()
	ctx := context.Background()

	// Empty query validation
	_, err := eng.Generate(ctx, "")
	if err == nil {
		t.Fatal("expected error for empty query, got nil")
	}

	// Empty ID validation
	err = eng.Apply(ctx, "", "admin-1")
	if err == nil {
		t.Fatal("expected error for empty id, got nil")
	}

	// Apply without approver requires approval
	err = eng.Apply(ctx, "rec-1", "")
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
}
