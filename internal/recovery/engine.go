package recovery

import (
	"context"
	"fmt"
)

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Recover(ctx context.Context, taskID, checkpointID string) (*Plan, error) {
	if taskID == "" {
		return nil, fmt.Errorf("taskID cannot be empty")
	}
	if checkpointID == "" {
		return nil, ErrNoValidCheckpoint
	}

	return &Plan{
		TaskID:       taskID,
		CheckpointID: checkpointID,
		RetryCount:   1,
		MaxRetries:   3,
		Action:       "RESUME_FROM_CHECKPOINT",
	}, nil
}
