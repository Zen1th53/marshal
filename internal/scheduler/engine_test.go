package scheduler

import (
	"context"
	"testing"
)

func TestSchedulerNext(t *testing.T) {
	sched := NewScheduler()
	ctx := context.Background()

	asgn, err := sched.Next(ctx, Task{TaskID: "task-1"}, []Candidate{{AgentID: "a-1"}})
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if asgn.AgentID != "a-1" {
		t.Fatalf("expected a-1, got %s", asgn.AgentID)
	}
}
