package router

import (
	"context"
	"errors"
	"testing"
)

func TestT15A04AdversarialBoundaries(t *testing.T) {
	rt := NewRouter()
	ctx := context.Background()

	// Excessive min context requirement
	_, err := rt.Route(ctx, []string{"code"}, 2000000)
	if !errors.Is(err, ErrContextTooSmall) {
		t.Fatalf("expected ErrContextTooSmall, got %v", err)
	}
}
