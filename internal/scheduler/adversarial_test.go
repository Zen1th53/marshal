package scheduler

import (
	"context"
	"errors"
	"testing"
)

func TestT03A04AdversarialBoundaries(t *testing.T) {
	sched := NewScheduler()
	ctx := context.Background()

	// No candidates available
	_, err := sched.Next(ctx, Task{TaskID: "task-1"}, nil)
	if !errors.Is(err, ErrNoEligibleAgent) {
		t.Fatalf("expected ErrNoEligibleAgent, got %v", err)
	}
}
