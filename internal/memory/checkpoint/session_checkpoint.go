package checkpoint

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	if err := ctx.Err(); err != nil {
		return SessionCheckpoint{}, err
	}
	if len(in.AttachedMemoryIDs) > 128 {
		return SessionCheckpoint{}, model.ErrInvalid
	}
	state, err := cloneSessionState(in.StateSnapshot)
	if err != nil {
		return SessionCheckpoint{}, err
	}
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
		AttachedMemoryIDs: append([]string(nil), in.AttachedMemoryIDs...),
		StateSnapshot:     state,
		CreatedAt:         now,
	}

	m.checkpoints[cp.CheckpointID] = &cp
	return cloneCheckpoint(cp)
}

// RestoreCheckpoint reloads session state and revalidates all attached memory references,
// advancing superseded records and omitting tombstoned memory.
func (m *Manager) RestoreCheckpoint(ctx context.Context, checkpointID string, resolver func(id string) (model.MemoryRecordV2, bool)) (RestoredSessionState, error) {
	if err := ctx.Err(); err != nil {
		return RestoredSessionState{}, err
	}
	if resolver == nil {
		return RestoredSessionState{}, model.ErrInvalid
	}
	m.mu.RLock()
	cp, ok := m.checkpoints[checkpointID]
	m.mu.RUnlock()

	if !ok {
		return RestoredSessionState{}, ErrCheckpointNotFound
	}

	var resolvedIDs []string
	seen := make(map[string]bool)

	for _, id := range cp.AttachedMemoryIDs {
		if err := ctx.Err(); err != nil {
			return RestoredSessionState{}, err
		}
		rec, found := resolver(id)
		if !found || rec.ProjectID != cp.ProjectID || rec.ID != id {
			continue
		}

		// 1. Omit tombstoned or rejected records
		if rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected {
			continue
		}

		// 2. Resolve superseded chain
		current := rec
		chain := map[string]bool{current.ID: true}
		for current.Lifecycle == model.MemorySuperseded && len(current.SupersededBy) > 0 {
			if err := ctx.Err(); err != nil {
				return RestoredSessionState{}, err
			}
			if len(current.SupersededBy) != 1 || len(chain) >= 128 {
				return RestoredSessionState{}, model.ErrConflict
			}
			nextID := current.SupersededBy[0]
			if chain[nextID] {
				return RestoredSessionState{}, fmt.Errorf("%w: cyclic memory supersession", model.ErrConflict)
			}
			chain[nextID] = true
			nextRec, nextFound := resolver(nextID)
			if !nextFound || nextRec.ProjectID != cp.ProjectID || nextRec.ID != nextID || nextRec.Lifecycle == model.MemoryTombstoned || nextRec.Lifecycle == model.MemoryRejected {
				break
			}
			current = nextRec
		}

		if (current.Lifecycle == model.MemoryCandidate || current.Lifecycle == model.MemoryVerified || current.Lifecycle == model.MemoryDurable) && !seen[current.ID] {
			seen[current.ID] = true
			resolvedIDs = append(resolvedIDs, current.ID)
		}
	}

	copy, err := cloneCheckpoint(*cp)
	if err != nil {
		return RestoredSessionState{}, err
	}
	return RestoredSessionState{
		Checkpoint:        copy,
		ResolvedMemoryIDs: resolvedIDs,
	}, nil
}

func cloneSessionState(in map[string]any) (map[string]any, error) {
	b, err := json.Marshal(in)
	if err != nil || len(b) > 65536 {
		return nil, model.ErrInvalid
	}
	var out map[string]any
	if err = json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func cloneCheckpoint(in SessionCheckpoint) (SessionCheckpoint, error) {
	in.AttachedMemoryIDs = append([]string(nil), in.AttachedMemoryIDs...)
	state, err := cloneSessionState(in.StateSnapshot)
	in.StateSnapshot = state
	return in, err
}
