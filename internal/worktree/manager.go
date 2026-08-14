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
