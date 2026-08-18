package compiler

import (
	"context"
	"errors"
	"testing"
)

func TestT11A04AdversarialBoundaries(t *testing.T) {
	comp := NewCompiler()
	ctx := context.Background()

	// Budget limit exceeded
	_, err := comp.Compile(ctx, "c-bad", "t-1", "a-1", "Long text prompt that exceeds budget limit", 2, nil, nil)
	if !errors.Is(err, ErrBudgetUnsatisfiable) {
		t.Fatalf("expected ErrBudgetUnsatisfiable, got %v", err)
	}

	// Secret rejected
	_, err = comp.Compile(ctx, "c-sec", "t-1", "a-1", "Includes secret_key = 12345", 1000, nil, nil)
	if !errors.Is(err, ErrSecretRejected) {
		t.Fatalf("expected ErrSecretRejected, got %v", err)
	}
}
