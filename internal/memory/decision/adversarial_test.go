package decision

import (
	"context"
	"errors"
	"testing"
)

func TestT09A04AdversarialBoundaries(t *testing.T) {
	eng := NewEngine()
	ctx := context.Background()
	_, _ = eng.Propose(ctx, "d-adv", "t-1", "a-1", "title", "ctx", "dec")

	// Missing authority
	if _, err := eng.Accept(ctx, "d-adv", ""); !errors.Is(err, ErrAuthorityRequired) {
		t.Fatalf("expected ErrAuthorityRequired, got %v", err)
	}

	// Invalid supersession
	if err := eng.Supersede(ctx, "d-adv", "nonexistent"); !errors.Is(err, ErrSupersessionInvalid) {
		t.Fatalf("expected ErrSupersessionInvalid, got %v", err)
	}
}
