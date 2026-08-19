package checkpoint

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrCheckpointNotFound = errors.New("checkpoint not found")
)

type SessionCheckpoint struct {
	CheckpointID      string         `json:"checkpoint_id"`
	ProjectID         string         `json:"project_id"`
	TaskID            string         `json:"task_id"`
	SessionID         string         `json:"session_id"`
	StepNumber        int            `json:"step_number"`
	AttachedMemoryIDs []string       `json:"attached_memory_ids"`
	StateSnapshot     map[string]any `json:"state_snapshot"`
	CreatedAt         time.Time      `json:"created_at"`
}

type CheckpointInput struct {
	ProjectID         string         `json:"project_id"`
	TaskID            string         `json:"task_id"`
	SessionID         string         `json:"session_id"`
	StepNumber        int            `json:"step_number"`
	AttachedMemoryIDs []string       `json:"attached_memory_ids"`
	StateSnapshot     map[string]any `json:"state_snapshot"`
	CreatedAt         time.Time      `json:"created_at"`
}

type RestoredSessionState struct {
	Checkpoint        SessionCheckpoint `json:"checkpoint"`
	ResolvedMemoryIDs []string          `json:"resolved_memory_ids"`
}

type Manager struct {
	mu          sync.RWMutex
	checkpoints map[string]*SessionCheckpoint
}

func NewManager() *Manager {
	return &Manager{
		checkpoints: make(map[string]*SessionCheckpoint),
	}
}

func generateCheckpointID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("CKPT-%s", hex.EncodeToString(b[:]))
}

// CreateCheckpoint stores a thread continuation snapshot referencing memory IDs without copying full payloads.
func (m *Manager) CreateCheckpoint(ctx context.Context, in CheckpointInput) (SessionCheckpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := in.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}

	cp := SessionCheckpoint{
		CheckpointID:      generateCheckpointID(),
		ProjectID:         in.ProjectID,
		TaskID:            in.TaskID,
		SessionID:         in.SessionID,
		StepNumber:        in.StepNumber,
		AttachedMemoryIDs: in.AttachedMemoryIDs,
		StateSnapshot:     in.StateSnapshot,
		CreatedAt:         now,
	}

	m.checkpoints[cp.CheckpointID] = &cp
	return cp, nil
}

// RestoreCheckpoint reloads session state and revalidates all attached memory references,
// advancing superseded records and omitting tombstoned memory.
func (m *Manager) RestoreCheckpoint(ctx context.Context, checkpointID string, resolver func(id string) (model.MemoryRecordV2, bool)) (RestoredSessionState, error) {
	m.mu.RLock()
	cp, ok := m.checkpoints[checkpointID]
	m.mu.RUnlock()

	if !ok {
		return RestoredSessionState{}, ErrCheckpointNotFound
	}

	var resolvedIDs []string
	seen := make(map[string]bool)

	for _, id := range cp.AttachedMemoryIDs {
		rec, found := resolver(id)
		if !found {
			continue
		}

		// 1. Omit tombstoned or rejected records
		if rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected {
			continue
		}

		// 2. Resolve superseded chain
		current := rec
		for current.Lifecycle == model.MemorySuperseded && len(current.SupersededBy) > 0 {
			nextID := current.SupersededBy[0]
			nextRec, nextFound := resolver(nextID)
			if !nextFound || nextRec.Lifecycle == model.MemoryTombstoned {
				break
			}
			current = nextRec
		}

		if current.Lifecycle != model.MemoryTombstoned && current.Lifecycle != model.MemoryRejected && !seen[current.ID] {
			seen[current.ID] = true
			resolvedIDs = append(resolvedIDs, current.ID)
		}
	}

	return RestoredSessionState{
		Checkpoint:        *cp,
		ResolvedMemoryIDs: resolvedIDs,
	}, nil
}
