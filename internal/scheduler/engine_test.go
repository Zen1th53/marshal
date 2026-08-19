package scheduler

import (
	"context"
	"errors"
	"testing"
)

func TestSchedulerNext(t *testing.T) {
	sched := NewScheduler()
	ctx := context.Background()

	asgn, err := sched.Next(ctx, Task{TaskID: "task-1"}, []Candidate{
		{AgentID: "a-1", SuccessRate: 0.9, Load: 0.1},
	})
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if asgn.AgentID != "a-1" {
		t.Fatalf("expected a-1, got %s", asgn.AgentID)
	}
	if asgn.Score <= 0 || asgn.Score > 1.0 {
		t.Fatalf("unexpected score: %f", asgn.Score)
	}
	if len(asgn.Reasons) == 0 {
		t.Fatal("expected structured reasons in assignment")
	}
}

func TestSchedulerCapabilityFiltering(t *testing.T) {
	sched := NewScheduler()
	ctx := context.Background()

	task := Task{
		TaskID:               "task-python",
		RequiredCapabilities: []string{"python", "git"},
	}

	candidates := []Candidate{
		{AgentID: "agent-no-caps", Load: 0.0, SuccessRate: 1.0, Capabilities: []string{}},
		{AgentID: "agent-python-only", Load: 0.0, SuccessRate: 1.0, Capabilities: []string{"python"}},
		{AgentID: "agent-qualified", Load: 0.5, SuccessRate: 0.8, Capabilities: []string{"python", "git", "docker"}},
	}

	asgn, err := sched.Next(ctx, task, candidates)
	if err != nil {
		t.Fatalf("expected successful scheduling, got error: %v", err)
	}
	if asgn.AgentID != "agent-qualified" {
		t.Fatalf("expected agent-qualified to win, got %s", asgn.AgentID)
	}
}

func TestSchedulerLoadAndSuccessRateOrdering(t *testing.T) {
	sched := NewScheduler()
	ctx := context.Background()
	task := Task{TaskID: "task-load-test"}

	// Scenario 1: Agent 1 has lower load and higher success rate -> Agent 1 wins
	cands := []Candidate{
		{AgentID: "agent-1", Load: 0.1, SuccessRate: 0.95, ContextUtilization: 0.2, EstimatedCost: 0.01},
		{AgentID: "agent-2", Load: 0.8, SuccessRate: 0.60, ContextUtilization: 0.8, EstimatedCost: 0.05},
	}

	asgn1, err := sched.Next(ctx, task, cands)
	if err != nil {
		t.Fatal(err)
	}
	if asgn1.AgentID != "agent-1" {
		t.Fatalf("expected agent-1 to win, got %s", asgn1.AgentID)
	}

	// Scenario 2: Agent 1 becomes heavily loaded (0.95) and Agent 2 is idle (0.1) with good success (0.90) -> Agent 2 wins
	cands[0].Load = 0.95
	cands[1].Load = 0.1
	cands[1].SuccessRate = 0.90

	asgn2, err := sched.Next(ctx, task, cands)
	if err != nil {
		t.Fatal(err)
	}
	if asgn2.AgentID != "agent-2" {
		t.Fatalf("expected agent-2 to win after load change, got %s", asgn2.AgentID)
	}
}

func TestSchedulerDeterministicTieBreaking(t *testing.T) {
	sched := NewScheduler()
	ctx := context.Background()
	task := Task{TaskID: "task-tie-test"}

	// Identical metrics for two agents
	cands := []Candidate{
		{AgentID: "agent-z", Load: 0.2, SuccessRate: 0.8, ContextUtilization: 0.3, EstimatedCost: 0.02},
		{AgentID: "agent-a", Load: 0.2, SuccessRate: 0.8, ContextUtilization: 0.3, EstimatedCost: 0.02},
	}

	asgn, err := sched.Next(ctx, task, cands)
	if err != nil {
		t.Fatal(err)
	}
	// "agent-a" comes first alphabetically
	if asgn.AgentID != "agent-a" {
		t.Fatalf("expected deterministic tie-breaker agent-a, got %s", asgn.AgentID)
	}
}

func TestSchedulerLeaseLifecycle(t *testing.T) {
	sched := NewScheduler()
	ctx := context.Background()

	asgn, err := sched.Next(ctx, Task{TaskID: "task-lease"}, []Candidate{
		{AgentID: "worker-1", Load: 0.1, SuccessRate: 0.9},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Renew valid lease
	if err := sched.Renew(ctx, asgn.LeaseID); err != nil {
		t.Fatalf("expected successful renewal, got: %v", err)
	}

	// Renew invalid / unknown lease fails
	if err := sched.Renew(ctx, "lease-unknown-999"); !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("expected ErrStaleWorker for unknown lease, got: %v", err)
	}

	// Release valid lease
	if err := sched.Release(ctx, asgn.LeaseID, "completed"); err != nil {
		t.Fatalf("expected successful release, got: %v", err)
	}

	// Releasing again fails because lease was removed
	if err := sched.Release(ctx, asgn.LeaseID, "completed"); !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("expected ErrStaleWorker for released lease, got: %v", err)
	}
}
