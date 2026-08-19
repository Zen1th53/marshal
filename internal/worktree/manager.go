package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var taskIDPattern = regexp.MustCompile(`^TASK-[A-Za-z0-9._-]+$`)

type Manager struct {
	repository string
	root       string
}

func New(repository, root string) *Manager {
	return &Manager{repository: repository, root: root}
}

func (m *Manager) Prepare(ctx context.Context, request model.WorktreeRequest) (model.Worktree, error) {
	if !taskIDPattern.MatchString(request.TaskID) || request.Branch == "" || request.BaseCommit == "" {
		return model.Worktree{}, fmt.Errorf("%w: incomplete worktree request", model.ErrInvalid)
	}
	repository, err := canonicalPath(m.repository)
	if err != nil {
		return model.Worktree{}, err
	}
	discovered, err := m.git(ctx, repository, "rev-parse", "--show-toplevel")
	if err != nil {
		return model.Worktree{}, err
	}
	discovered, err = canonicalPath(discovered)
	if err != nil {
		return model.Worktree{}, err
	}
	if discovered != repository {
		return model.Worktree{}, fmt.Errorf("%w: repository root mismatch", model.ErrConflict)
	}
	if _, err := m.git(ctx, repository, "rev-parse", "--verify", request.BaseCommit+"^{commit}"); err != nil {
		return model.Worktree{}, fmt.Errorf("%w: base commit is unavailable: %v", model.ErrConflict, err)
	}
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return model.Worktree{}, fmt.Errorf("create worktree root: %w", err)
	}
	if err := os.Chmod(m.root, 0o700); err != nil {
		return model.Worktree{}, fmt.Errorf("secure worktree root: %w", err)
	}
	target := filepath.Join(m.root, request.TaskID)
	if !pathWithin(m.root, target) {
		return model.Worktree{}, fmt.Errorf("%w: worktree target escapes root", model.ErrInvalid)
	}
	if _, err := os.Lstat(target); err == nil {
		state, inspectErr := m.Inspect(ctx, target)
		if inspectErr != nil {
			return model.Worktree{}, fmt.Errorf("%w: preserve unexpected target %s: %v", model.ErrConflict, target, inspectErr)
		}
		if state.Branch != request.Branch || state.HEAD != request.BaseCommit {
			return model.Worktree{}, fmt.Errorf("%w: existing worktree identity differs", model.ErrConflict)
		}
		if state.Dirty {
			return model.Worktree{}, fmt.Errorf("%w: existing task worktree", model.ErrDirtyWorktree)
		}
		return model.Worktree{TaskID: request.TaskID, Path: state.Path, Branch: state.Branch, HEAD: state.HEAD}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return model.Worktree{}, fmt.Errorf("inspect worktree target: %w", err)
	}
	if _, err := m.git(ctx, repository, "show-ref", "--verify", "--quiet", "refs/heads/"+request.Branch); err == nil {
		return model.Worktree{}, fmt.Errorf("%w: branch %s already exists without its task worktree", model.ErrConflict, request.Branch)
	}
	if _, err := m.git(ctx, repository, "worktree", "add", "-b", request.Branch, target, request.BaseCommit); err != nil {
		return model.Worktree{}, fmt.Errorf("create task worktree: %w", err)
	}
	state, err := m.Inspect(ctx, target)
	if err != nil {
		return model.Worktree{}, err
	}
	return model.Worktree{TaskID: request.TaskID, Path: state.Path, Branch: state.Branch, HEAD: state.HEAD, Dirty: state.Dirty}, nil
}

func (m *Manager) Inspect(ctx context.Context, path string) (model.WorktreeState, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return model.WorktreeState{}, err
	}
	if !pathWithin(m.root, canonical) {
		return model.WorktreeState{}, fmt.Errorf("%w: worktree lies outside runtime root", model.ErrConflict)
	}
	root, err := m.git(ctx, canonical, "rev-parse", "--show-toplevel")
	if err != nil {
		return model.WorktreeState{}, err
	}
	root, err = canonicalPath(root)
	if err != nil {
		return model.WorktreeState{}, err
	}
	if root != canonical {
		return model.WorktreeState{}, fmt.Errorf("%w: path is not a worktree root", model.ErrConflict)
	}
	branch, err := m.git(ctx, canonical, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return model.WorktreeState{}, err
	}
	head, err := m.git(ctx, canonical, "rev-parse", "HEAD")
	if err != nil {
		return model.WorktreeState{}, err
	}
	status, err := m.git(ctx, canonical, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return model.WorktreeState{}, err
	}
	return model.WorktreeState{Path: canonical, Branch: branch, HEAD: head, Dirty: status != ""}, nil
}

func (m *Manager) Remove(ctx context.Context, worktree model.Worktree) error {
	state, err := m.Inspect(ctx, worktree.Path)
	if err != nil {
		return err
	}
	if state.Dirty {
		return fmt.Errorf("%w: refusing to remove %s", model.ErrDirtyWorktree, state.Path)
	}
	if worktree.Branch != state.Branch {
		return fmt.Errorf("%w: worktree branch changed", model.ErrConflict)
	}
	if _, err := m.git(ctx, m.repository, "worktree", "remove", state.Path); err != nil {
		return fmt.Errorf("remove clean task worktree: %w", err)
	}
	return nil
}

func (m *Manager) git(ctx context.Context, directory string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(stderr.Bytes()))
	}
	return strings.TrimSpace(string(output)), nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make path absolute: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve path %s: %w", absolute, err)
	}
	return resolved, nil
}

func pathWithin(root, candidate string) bool {
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidateAbsolute, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(rootAbsolute, candidateAbsolute)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// CalculateDirectorySize returns the total file size in bytes for the specified directory path.
func CalculateDirectorySize(path string) (int64, error) {
	var totalSize int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	return totalSize, err
}

type RetentionState string

const (
	RetentionActive         RetentionState = "active"
	RetentionReviewRetained RetentionState = "review-retained"
	RetentionMergedCleanup  RetentionState = "merged-cleanup"
	RetentionFailedDebug    RetentionState = "failed-retained-for-debug"
	RetentionExpired        RetentionState = "expired"
)

type GCRequest struct {
	DryRun       bool                        `json:"dry_run"`
	TTL          time.Duration               `json:"ttl"`
	ActiveLeases []string                    `json:"active_leases,omitempty"`
	TaskStatuses map[string]model.TaskStatus `json:"task_statuses,omitempty"`
}

type GCResult struct {
	InspectedCount int      `json:"inspected_count"`
	CleanedPaths   []string `json:"cleaned_paths,omitempty"`
	RetainedPaths  []string `json:"retained_paths,omitempty"`
	SkippedDirty   []string `json:"skipped_dirty,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

func (m *Manager) GC(ctx context.Context, req GCRequest) (GCResult, error) {
	result := GCResult{}
	entries, err := os.ReadDir(m.root)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read worktrees root: %w", err)
	}

	activeLeaseMap := make(map[string]bool, len(req.ActiveLeases))
	for _, l := range req.ActiveLeases {
		activeLeaseMap[l] = true
	}

	now := time.Now().UTC()
	ttl := req.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		result.InspectedCount++
		taskID := entry.Name()
		targetPath := filepath.Join(m.root, taskID)

		// 1. Active lease must never be removed
		if activeLeaseMap[taskID] {
			result.RetainedPaths = append(result.RetainedPaths, targetPath)
			continue
		}

		// 2. Inspect worktree git state
		state, err := m.Inspect(ctx, targetPath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: inspect failed: %v", targetPath, err))
			result.RetainedPaths = append(result.RetainedPaths, targetPath)
			continue
		}

		// 3. Dirty uncommitted worktree must never be deleted (fail-closed)
		if state.Dirty {
			result.SkippedDirty = append(result.SkippedDirty, targetPath)
			result.RetainedPaths = append(result.RetainedPaths, targetPath)
			continue
		}

		// 4. Determine retention state based on task status and TTL
		status, hasStatus := req.TaskStatuses[taskID]
		canClean := false

		if hasStatus {
			switch status {
			case model.TaskMerged, model.TaskCancelled, model.TaskSuperseded:
				canClean = true
			case model.TaskWorking, model.TaskReview, model.TaskQA, model.TaskSecurityReview, model.TaskReadyToMerge:
				canClean = false
			default:
				canClean = false
			}
		}

		// 5. Check directory age against TTL if not explicitly retained
		if !canClean {
			info, err := entry.Info()
			if err == nil && now.Sub(info.ModTime()) > ttl {
				canClean = true
			}
		}

		if canClean {
			if !req.DryRun {
				if err := m.Remove(ctx, model.Worktree{Path: state.Path, Branch: state.Branch}); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("%s: remove failed: %v", targetPath, err))
					result.RetainedPaths = append(result.RetainedPaths, targetPath)
					continue
				}
			}
			result.CleanedPaths = append(result.CleanedPaths, targetPath)
		} else {
			result.RetainedPaths = append(result.RetainedPaths, targetPath)
		}
	}

	return result, nil
}
