package scheduler

import (
	"context"
	"errors"
	"testing"
)

func TestT03A04AdversarialBoundaries(t *testing.T) {
	sched := NewScheduler()
	ctx := context.Background()

	// Empty TaskID
	_, err := sched.Next(ctx, Task{}, []Candidate{{AgentID: "a-1"}})
	if !errors.Is(err, ErrTaskNotReady) {
		t.Fatalf("expected ErrTaskNotReady, got %v", err)
	}

	// No candidates available
	_, err = sched.Next(ctx, Task{TaskID: "task-1"}, nil)
	if !errors.Is(err, ErrNoEligibleAgent) {
		t.Fatalf("expected ErrNoEligibleAgent, got %v", err)
	}

	// Candidates with only empty AgentID
	_, err = sched.Next(ctx, Task{TaskID: "task-1"}, []Candidate{{AgentID: ""}})
	if !errors.Is(err, ErrNoEligibleAgent) {
		t.Fatalf("expected ErrNoEligibleAgent, got %v", err)
	}
}
