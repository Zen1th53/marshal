package recovery

import (
	"context"
	"errors"
	"testing"
)

func TestT14A04AdversarialBoundaries(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	// Missing checkpoint ID
	_, err := mgr.Recover(ctx, "task-1", "")
	if !errors.Is(err, ErrNoValidCheckpoint) {
		t.Fatalf("expected ErrNoValidCheckpoint, got %v", err)
	}

	// Non-existent checkpoint ID
	_, err = mgr.Recover(ctx, "task-1", "nonexistent-cp")
	if !errors.Is(err, ErrNoValidCheckpoint) {
		t.Fatalf("expected ErrNoValidCheckpoint for nonexistent checkpoint, got %v", err)
	}

	// Empty task ID
	_, err = mgr.Recover(ctx, "", "cp-1")
	if err == nil {
		t.Fatal("expected error for empty taskID, got nil")
	}
}
