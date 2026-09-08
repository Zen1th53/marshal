package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// CheckpointEngine handles durable checkpointing and atomic rollback.
type CheckpointEngine struct {
	mu           sync.RWMutex
	projectRoot  string
	backupDir    string
	isGitRepo    bool
	checkpoints  map[string]CheckpointRecord // checkpointID -> CheckpointRecord
}

// NewCheckpointEngine creates a new CheckpointEngine.
func NewCheckpointEngine(projectRoot string) (*CheckpointEngine, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve project root: %w", err)
	}

	backupDir := filepath.Join(absRoot, ".marshal", "checkpoints")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create checkpoints dir: %w", err)
	}

	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = absRoot
	isGitRepo := cmd.Run() == nil

	return &CheckpointEngine{
		projectRoot: absRoot,
		backupDir:   backupDir,
		isGitRepo:   isGitRepo,
		checkpoints: make(map[string]CheckpointRecord),
	}, nil
}

// CaptureCheckpoint snapshots the current state of the workspace.
func (ce *CheckpointEngine) CaptureCheckpoint(ctx context.Context, runID, taskID, reason string) (CheckpointRecord, error) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	now := time.Now().UTC()
	cpID := fmt.Sprintf("cp-%s-%s-%d", runID, taskID, now.UnixNano())

	var gitCommit string
	if ce.isGitRepo {
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
		cmd.Dir = ce.projectRoot
		out, err := cmd.Output()
		if err == nil {
			gitCommit = string(out)
		}
	}

	// Create backup snapshot directory
	cpDir := filepath.Join(ce.backupDir, cpID)
	if err := copyDir(ce.projectRoot, cpDir, []string{".git", ".marshal"}); err != nil {
		return CheckpointRecord{}, fmt.Errorf("%w: failed to snapshot directory: %v", ErrCheckpointFailed, err)
	}

	h := sha256.New()
	fmt.Fprintf(h, "%s:%s:%s:%s:%d", runID, taskID, gitCommit, reason, now.UnixNano())
	stateDigest := hex.EncodeToString(h.Sum(nil))

	rec := CheckpointRecord{
		CheckpointID: cpID,
		RunID:        runID,
		TaskID:       taskID,
		ProjectID:    ce.projectRoot,
		GitCommit:    gitCommit,
		WorktreePath: cpDir,
		StateDigest:  stateDigest,
		Reason:       reason,
		CreatedAt:    now,
	}

	ce.checkpoints[cpID] = rec
	recBytes, _ := json.MarshalIndent(rec, "", "  ")
	_ = os.WriteFile(filepath.Join(ce.backupDir, cpID+".json"), recBytes, 0644)

	return rec, nil
}

// RestoreCheckpoint rolls back the workspace to the specified checkpoint.
func (ce *CheckpointEngine) RestoreCheckpoint(ctx context.Context, checkpointID string) (CheckpointRecord, error) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	rec, exists := ce.checkpoints[checkpointID]
	if !exists {
		data, err := os.ReadFile(filepath.Join(ce.backupDir, checkpointID+".json"))
		if err != nil {
			return CheckpointRecord{}, fmt.Errorf("%w: checkpoint %s not found", ErrCheckpointFailed, checkpointID)
		}
		if err := json.Unmarshal(data, &rec); err != nil {
			return CheckpointRecord{}, fmt.Errorf("%w: failed to parse checkpoint record: %v", ErrCheckpointFailed, err)
		}
	}

	// Restore files from snapshot
	if rec.WorktreePath != "" {
		fi, err := os.Stat(rec.WorktreePath)
		if err != nil {
			return CheckpointRecord{}, fmt.Errorf("%w: snapshot path invalid: %v", ErrCheckpointFailed, err)
		}
		if !fi.IsDir() {
			return CheckpointRecord{}, fmt.Errorf("%w: snapshot path is not a directory", ErrCheckpointFailed)
		}
		if err := copyDir(rec.WorktreePath, ce.projectRoot, []string{".git", ".marshal"}); err != nil {
			return CheckpointRecord{}, fmt.Errorf("%w: failed to restore snapshot: %v", ErrCheckpointFailed, err)
		}
	}

	now := time.Now().UTC()
	rec.RestoredAt = &now
	ce.checkpoints[checkpointID] = rec
	recBytes, _ := json.MarshalIndent(rec, "", "  ")
	_ = os.WriteFile(filepath.Join(ce.backupDir, checkpointID+".json"), recBytes, 0644)

	return rec, nil
}

// GetCheckpoint retrieves a checkpoint by ID.
func (ce *CheckpointEngine) GetCheckpoint(checkpointID string) (CheckpointRecord, error) {
	ce.mu.RLock()
	rec, exists := ce.checkpoints[checkpointID]
	ce.mu.RUnlock()

	if !exists {
		data, err := os.ReadFile(filepath.Join(ce.backupDir, checkpointID+".json"))
		if err != nil {
			return CheckpointRecord{}, fmt.Errorf("%w: checkpoint %s not found", ErrCheckpointFailed, checkpointID)
		}
		if err := json.Unmarshal(data, &rec); err != nil {
			return CheckpointRecord{}, fmt.Errorf("%w: failed to parse checkpoint record: %v", ErrCheckpointFailed, err)
		}
		ce.mu.Lock()
		ce.checkpoints[checkpointID] = rec
		ce.mu.Unlock()
	}
	return rec, nil
}
