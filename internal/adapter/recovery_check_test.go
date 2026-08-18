package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/recovery"
)

func TestAgentAutoRecoveryServiceAdapter(t *testing.T) {
	mgr := recovery.NewManager()
	ctx := context.Background()
	svc := NewAgentAutoRecoveryService(mgr)

	plan, err := svc.RecoverTask(ctx, "t-1", "cp-1")
	if err != nil {
		t.Fatalf("RecoverTask failed: %v", err)
	}
	if plan.TaskID != "t-1" {
		t.Fatalf("expected t-1, got %s", plan.TaskID)
	}
}
