package recovery

import (
	"context"
	"testing"
)

func TestManagerRecover(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	plan, err := mgr.Recover(ctx, "task-1", "cp-1")
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if plan.Action != "RESUME_FROM_CHECKPOINT" {
		t.Fatalf("expected RESUME_FROM_CHECKPOINT, got %s", plan.Action)
	}
}
