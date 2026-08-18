package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/scheduler"
)

type AgentSchedulerService struct {
	scheduler *scheduler.Scheduler
}

func NewAgentSchedulerService(scheduler *scheduler.Scheduler) *AgentSchedulerService {
	return &AgentSchedulerService{scheduler: scheduler}
}

func (s *AgentSchedulerService) ScheduleTask(ctx context.Context, taskID string, agents []scheduler.Candidate) (*scheduler.Assignment, error) {
	if s == nil || s.scheduler == nil {
		return nil, fmt.Errorf("scheduler service uninitialized")
	}
	return s.scheduler.Next(ctx, scheduler.Task{TaskID: taskID}, agents)
}
