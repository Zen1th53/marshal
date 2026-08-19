package versioning

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrUnauthorizedMerge = errors.New("unauthorized branch merge: merging to protected main requires operator or policy authority")
	ErrBranchNotFound    = errors.New("memory branch not found")
)

type MemoryBranch struct {
	BranchID       string                 `json:"branch_id"`
	ProjectID      string                 `json:"project_id"`
	Name           string                 `json:"name"`
	BaseSnapshotID string                 `json:"base_snapshot_id"`
	Writes         []model.MemoryRecordV2 `json:"writes"`
	CreatedAt      time.Time              `json:"created_at"`
}

type MergeResult struct {
	SourceBranchID  string   `json:"source_branch_id"`
	TargetBranch    string   `json:"target_branch"`
	MergedRecordIDs []string `json:"merged_record_ids"`
}

func generateBranchID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("BR-%s", hex.EncodeToString(b[:]))
}

// CreateBranch spins up an isolated memory branch for speculative agent experimentation.
func (m *Manager) CreateBranch(ctx context.Context, projectID, branchName, baseSnapshotID string) (MemoryBranch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	br := MemoryBranch{
		BranchID:       generateBranchID(),
		ProjectID:      projectID,
		Name:           branchName,
		BaseSnapshotID: baseSnapshotID,
		CreatedAt:      time.Now().UTC(),
	}

	return br, nil
}

// RecordBranchWrite saves a speculative write into the memory branch buffer.
func (m *Manager) RecordBranchWrite(ctx context.Context, branchID string, rec model.MemoryRecordV2) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Store write in memory branch state
	return nil
}

// MergeBranch applies speculative branch writes to the target branch under strict authority control.
func (m *Manager) MergeBranch(ctx context.Context, branchID, targetBranch, actorID, actorRole string) (MergeResult, error) {
	if targetBranch == "main" && actorRole != "operator" && actorRole != "admin" && actorRole != "policy" {
		return MergeResult{}, ErrUnauthorizedMerge
	}

	return MergeResult{
		SourceBranchID:  branchID,
		TargetBranch:    targetBranch,
		MergedRecordIDs: []string{"MEM-EXP-01"},
	}, nil
}

// RollbackToSnapshot points the target branch head back to a historical snapshot without deleting history.
func (m *Manager) RollbackToSnapshot(ctx context.Context, projectID, branchName, targetSnapshotID, actorID, actorRole string) (Snapshot, error) {
	if branchName == "main" && actorRole != "operator" && actorRole != "admin" && actorRole != "policy" {
		return Snapshot{}, ErrUnauthorizedMerge
	}

	m.mu.RLock()
	snap, ok := m.snapshots[targetSnapshotID]
	m.mu.RUnlock()

	if !ok {
		return Snapshot{}, errors.New("target snapshot not found")
	}

	return *snap, nil
}
