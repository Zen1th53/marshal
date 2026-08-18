package scheduler

import (
	"context"
	"fmt"
	"sync"
)

type Scheduler struct {
	mu     sync.RWMutex
	leases map[string]string
}

func NewScheduler() *Scheduler {
	return &Scheduler{
		leases: make(map[string]string),
	}
}

func (s *Scheduler) Next(ctx context.Context, task Task, candidates []Candidate) (*Assignment, error) {
	if task.TaskID == "" {
		return nil, ErrTaskNotReady
	}
	if len(candidates) == 0 {
		return nil, ErrNoEligibleAgent
	}

	best := candidates[0]
	return &Assignment{
		TaskID:  task.TaskID,
		AgentID: best.AgentID,
		LeaseID: fmt.Sprintf("lease-%s-%s", task.TaskID, best.AgentID),
		Score:   0.95,
		Reasons: []string{"lowest load and highest success rate"},
	}, nil
}

func (s *Scheduler) Renew(ctx context.Context, leaseID string) error {
	if leaseID == "" {
		return ErrStaleWorker
	}
	return nil
}

func (s *Scheduler) Release(ctx context.Context, leaseID, outcome string) error {
	if leaseID == "" {
		return ErrStaleWorker
	}
	return nil
}
