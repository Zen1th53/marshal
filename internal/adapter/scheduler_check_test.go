package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/scheduler"
)

func TestAgentSchedulerServiceAdapter(t *testing.T) {
	sched := scheduler.NewScheduler()
	ctx := context.Background()
	svc := NewAgentSchedulerService(sched)

	asgn, err := svc.ScheduleTask(ctx, "t-1", []scheduler.Candidate{{AgentID: "a-1"}})
	if err != nil {
		t.Fatalf("ScheduleTask failed: %v", err)
	}
	if asgn.AgentID != "a-1" {
		t.Fatalf("expected a-1, got %s", asgn.AgentID)
	}
}
