package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/recovery"
)

type AgentAutoRecoveryService struct {
	manager *recovery.Manager
}

func NewAgentAutoRecoveryService(manager *recovery.Manager) *AgentAutoRecoveryService {
	return &AgentAutoRecoveryService{manager: manager}
}

func (s *AgentAutoRecoveryService) RecoverTask(ctx context.Context, taskID, checkpointID string) (*recovery.Plan, error) {
	if s == nil || s.manager == nil {
		return nil, fmt.Errorf("recovery service uninitialized")
	}
	return s.manager.Recover(ctx, taskID, checkpointID)
}
