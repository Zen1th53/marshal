package recovery

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

type Manager struct {
	mu          sync.RWMutex
	checkpoints map[string]Checkpoint
}

func NewManager() *Manager {
	return &Manager{
		checkpoints: make(map[string]Checkpoint),
	}
}

func (m *Manager) RegisterCheckpoint(cp Checkpoint) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints[cp.ID] = cp
}

func (m *Manager) Recover(ctx context.Context, taskID, checkpointID string) (*Plan, error) {
	if taskID == "" {
		return nil, fmt.Errorf("taskID cannot be empty")
	}
	if checkpointID == "" {
		return nil, ErrNoValidCheckpoint
	}

	m.mu.RLock()
	cp, found := m.checkpoints[checkpointID]
	m.mu.RUnlock()

	var cpPtr *Checkpoint
	if found {
		cpPtr = &cp
	} else {
		// Default synthetic valid checkpoint if not explicitly registered
		cpPtr = &Checkpoint{
			ID:        checkpointID,
			TaskID:    taskID,
			State:     CheckpointValid,
			CreatedAt: time.Now().UTC(),
		}
	}

	return m.PlanRecovery(ctx, RecoveryRequest{
		TaskID:         taskID,
		Checkpoint:     cpPtr,
		CurrentRetries: 0,
		MaxRetries:     3,
	})
}

func (m *Manager) PlanRecovery(ctx context.Context, req RecoveryRequest) (*Plan, error) {
	if req.TaskID == "" {
		return nil, fmt.Errorf("taskID cannot be empty")
	}

	maxRetries := req.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}

	// 1. Guard against concurrent active lease owner
	if req.ActiveLeaseOwner != "" {
		return nil, ErrConcurrentOwner
	}

	// 2. Check retry budget
	if req.CurrentRetries >= maxRetries {
		return nil, ErrRetryExhausted
	}

	retryCount := req.CurrentRetries + 1
	// Exponential backoff: min(60, 2^(retryCount-1))
	backoff := int(math.Min(60, math.Pow(2, float64(retryCount-1))))

	// 3. Handle poisoned or corrupt checkpoints
	if req.Checkpoint != nil && req.Checkpoint.State == CheckpointPoisoned {
		return &Plan{
			TaskID:         req.TaskID,
			RetryCount:     retryCount,
			MaxRetries:     maxRetries,
			Action:         ActionRestartFromBase,
			BackoffSeconds: backoff,
			Reasons: []string{
				"checkpoint marked poisoned; forcing clean restart from base commit",
				fmt.Sprintf("failure_type=%s", req.Failure),
				fmt.Sprintf("retry_attempt=%d/%d", retryCount, maxRetries),
			},
		}, nil
	}

	if req.Checkpoint != nil && req.Checkpoint.State == CheckpointCorrupt {
		return &Plan{
			TaskID:         req.TaskID,
			RetryCount:     retryCount,
			MaxRetries:     maxRetries,
			Action:         ActionRestartFromBase,
			BackoffSeconds: backoff,
			Reasons: []string{
				"checkpoint corrupted; falling back to restart from base commit",
				fmt.Sprintf("failure_type=%s", req.Failure),
				fmt.Sprintf("retry_attempt=%d/%d", retryCount, maxRetries),
			},
		}, nil
	}

	// 4. If clean checkpoint exists and force restart is not requested -> Resume from checkpoint
	if req.Checkpoint != nil && req.Checkpoint.ID != "" && !req.ForceRestart {
		return &Plan{
			TaskID:         req.TaskID,
			CheckpointID:   req.Checkpoint.ID,
			RetryCount:     retryCount,
			MaxRetries:     maxRetries,
			Action:         ActionResumeFromCheckpoint,
			BackoffSeconds: backoff,
			Reasons: []string{
				fmt.Sprintf("valid checkpoint %s found", req.Checkpoint.ID),
				fmt.Sprintf("failure_classification=%s", req.Failure),
				fmt.Sprintf("retry_attempt=%d/%d", retryCount, maxRetries),
			},
		}, nil
	}

	// 5. Default fallback -> Restart from base commit
	return &Plan{
		TaskID:         req.TaskID,
		RetryCount:     retryCount,
		MaxRetries:     maxRetries,
		Action:         ActionRestartFromBase,
		BackoffSeconds: backoff,
		Reasons: []string{
			"no valid checkpoint provided; restarting from base commit",
			fmt.Sprintf("failure_classification=%s", req.Failure),
			fmt.Sprintf("retry_attempt=%d/%d", retryCount, maxRetries),
		},
	}, nil
}
