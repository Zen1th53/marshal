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
}
