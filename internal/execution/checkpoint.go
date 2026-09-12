package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CheckpointEngine handles durable checkpointing and atomic rollback.
type CheckpointEngine struct {
	mu          sync.RWMutex
	projectRoot string
	backupDir   string
	isGitRepo   bool
	checkpoints map[string]CheckpointRecord // checkpointID -> CheckpointRecord
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
	snapshotDigest, err := digestSnapshot(cpDir)
	if err != nil {
		return CheckpointRecord{}, fmt.Errorf("%w: failed to digest snapshot: %v", ErrCheckpointFailed, err)
	}

	rec := CheckpointRecord{
		CheckpointID:   cpID,
		RunID:          runID,
		TaskID:         taskID,
		ProjectID:      ce.projectRoot,
		GitCommit:      gitCommit,
		WorktreePath:   cpDir,
		SnapshotDigest: snapshotDigest,
		Reason:         reason,
		CreatedAt:      now,
	}
	rec.StateDigest = checkpointStateDigest(rec)

	if err := ce.persistRecord(rec); err != nil {
		_ = os.RemoveAll(cpDir)
		return CheckpointRecord{}, fmt.Errorf("%w: persist checkpoint record: %v", ErrCheckpointFailed, err)
	}
	ce.checkpoints[cpID] = rec

	return rec, nil
}

// RestoreCheckpoint rolls back the workspace to the specified checkpoint.
func (ce *CheckpointEngine) RestoreCheckpoint(ctx context.Context, checkpointID string) (CheckpointRecord, error) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	rec, exists := ce.checkpoints[checkpointID]
	if !exists {
		if err := validCheckpointID(checkpointID); err != nil {
			return CheckpointRecord{}, err
		}
		data, err := os.ReadFile(filepath.Join(ce.backupDir, checkpointID+".json"))
		if err != nil {
			return CheckpointRecord{}, fmt.Errorf("%w: checkpoint %s not found", ErrCheckpointFailed, checkpointID)
		}
		if err := json.Unmarshal(data, &rec); err != nil {
			return CheckpointRecord{}, fmt.Errorf("%w: failed to parse checkpoint record: %v", ErrCheckpointFailed, err)
		}
	}
	if err := ce.validateRecord(rec, checkpointID); err != nil {
		return CheckpointRecord{}, err
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
		if rec.SnapshotDigest == "" {
			return CheckpointRecord{}, fmt.Errorf("%w: checkpoint has no content digest and cannot be restored safely", ErrCheckpointFailed)
		}
		actualDigest, err := digestSnapshot(rec.WorktreePath)
		if err != nil {
			return CheckpointRecord{}, fmt.Errorf("%w: failed to verify snapshot: %v", ErrCheckpointFailed, err)
		}
		if actualDigest != rec.SnapshotDigest {
			return CheckpointRecord{}, fmt.Errorf("%w: snapshot digest mismatch", ErrCheckpointFailed)
		}
		if err := restoreSnapshot(ctx, rec.WorktreePath, ce.projectRoot, []string{".git", ".marshal"}); err != nil {
			return CheckpointRecord{}, fmt.Errorf("%w: failed to restore snapshot: %v", ErrCheckpointFailed, err)
		}
	}

	now := time.Now().UTC()
	rec.RestoredAt = &now
	if err := ce.persistRecord(rec); err != nil {
		return CheckpointRecord{}, fmt.Errorf("%w: persist restore record: %v", ErrCheckpointFailed, err)
	}
	ce.checkpoints[checkpointID] = rec

	return rec, nil
}

// restoreSnapshot makes the live, mutable project tree exactly match a
// verified checkpoint.  copyDir alone leaves files created after the
// checkpoint behind, which is not a rollback.  MARSHAL's own metadata and Git
// directory are deliberately retained; both are outside the captured tree.
func restoreSnapshot(ctx context.Context, src, dst string, skips []string) error {
	if err := copyDir(src, dst, skips); err != nil {
		return err
	}

	skip := make(map[string]struct{}, len(skips))
	for _, name := range skips {
		skip[name] = struct{}{}
	}
	var stale []string
	err := filepath.Walk(dst, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(dst, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		parts := strings.Split(rel, string(filepath.Separator))
		for _, part := range parts {
			if _, ok := skip[part]; ok {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if _, err := os.Lstat(filepath.Join(src, rel)); err != nil {
			if os.IsNotExist(err) {
				stale = append(stale, path)
				return nil
			}
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Children must go first.  RemoveAll is safe for a path discovered below
	// dst and checked to be absent from src; sorting also handles nested stale
	// directories deterministically.
	sort.Slice(stale, func(i, j int) bool { return len(stale[i]) > len(stale[j]) })
	for _, path := range stale {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

// digestSnapshot computes a stable content digest over relative paths, modes,
// and file bytes. filepath.Walk visits entries lexically, so the same captured
// tree yields the same digest across process restarts.
func digestSnapshot(root string) (string, error) {
	h := sha256.New()
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			_, err = fmt.Fprintf(h, "dir:%s:%o\n", rel, info.Mode().Perm())
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported snapshot entry %s (%s)", rel, info.Mode())
		}
		if _, err := fmt.Fprintf(h, "file:%s:%o:%d\n", rel, info.Mode().Perm(), info.Size()); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// GetCheckpoint retrieves a checkpoint by ID.
func (ce *CheckpointEngine) GetCheckpoint(checkpointID string) (CheckpointRecord, error) {
	ce.mu.RLock()
	rec, exists := ce.checkpoints[checkpointID]
	ce.mu.RUnlock()

	if !exists {
		if err := validCheckpointID(checkpointID); err != nil {
			return CheckpointRecord{}, err
		}
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
	if err := ce.validateRecord(rec, checkpointID); err != nil {
		return CheckpointRecord{}, err
	}
	return rec, nil
}

func checkpointStateDigest(rec CheckpointRecord) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s:%s:%s:%s:%s:%s:%s:%d", rec.RunID, rec.TaskID,
		rec.ProjectID, rec.WorktreePath, rec.GitCommit, rec.SnapshotDigest,
		rec.Reason, rec.CreatedAt.UnixNano())
	return hex.EncodeToString(h.Sum(nil))
}

func validCheckpointID(id string) error {
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." {
		return fmt.Errorf("%w: invalid checkpoint identifier", ErrCheckpointFailed)
	}
	return nil
}

func (ce *CheckpointEngine) validateRecord(rec CheckpointRecord, requestedID string) error {
	if err := validCheckpointID(requestedID); err != nil {
		return err
	}
	if rec.CheckpointID != requestedID {
		return fmt.Errorf("%w: checkpoint record identifier mismatch", ErrCheckpointFailed)
	}
	if rec.ProjectID != ce.projectRoot {
		return fmt.Errorf("%w: checkpoint project binding mismatch", ErrCheckpointFailed)
	}
	expectedPath := filepath.Join(ce.backupDir, requestedID)
	if filepath.Clean(rec.WorktreePath) != expectedPath {
		return fmt.Errorf("%w: checkpoint snapshot path escaped its durable binding", ErrCheckpointFailed)
	}
	if rec.StateDigest == "" || rec.StateDigest != checkpointStateDigest(rec) {
		return fmt.Errorf("%w: checkpoint metadata digest mismatch", ErrCheckpointFailed)
	}
	return nil
}

func (ce *CheckpointEngine) persistRecord(rec CheckpointRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(ce.backupDir, rec.CheckpointID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
