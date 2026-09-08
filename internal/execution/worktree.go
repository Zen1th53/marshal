package execution

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// WorktreeManager manages isolated working directories and git worktrees for tasks.
type WorktreeManager struct {
	mu           sync.Mutex
	projectRoot  string
	worktreesDir string
	isGitRepo    bool
}

// NewWorktreeManager creates a new WorktreeManager for the project root.
func NewWorktreeManager(projectRoot string) (*WorktreeManager, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve project root: %w", err)
	}

	// Verify project root exists and is a directory
	fi, err := os.Stat(absRoot)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("project root %s does not exist or is not a directory", absRoot)
	}

	worktreesDir := filepath.Join(absRoot, ".marshal", "worktrees")
	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create marshal worktrees directory: %w", err)
	}

	// Check if projectRoot is inside a git repository
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = absRoot
	isGitRepo := cmd.Run() == nil

	return &WorktreeManager{
		projectRoot:  absRoot,
		worktreesDir: worktreesDir,
		isGitRepo:    isGitRepo,
	}, nil
}

// ValidateTargetPaths ensures that target files do not escape the project boundary.
func (wm *WorktreeManager) ValidateTargetPaths(paths []string) error {
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		cleanPath := filepath.Clean(p)
		if filepath.IsAbs(cleanPath) {
			if !strings.HasPrefix(cleanPath, wm.projectRoot+"/") && cleanPath != wm.projectRoot {
				return fmt.Errorf("%w: absolute path %s escapes project root %s",
					ErrIsolationCompromised, cleanPath, wm.projectRoot)
			}
		} else {
			if strings.HasPrefix(cleanPath, "..") || strings.Contains(cleanPath, "/../") {
				return fmt.Errorf("%w: path traversal detected in %s", ErrIsolationCompromised, cleanPath)
			}
		}
	}
	return nil
}

// PrepareWorktree sets up an isolated workspace for a task.
func (wm *WorktreeManager) PrepareWorktree(ctx context.Context, taskID, runID string) (string, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	wtName := fmt.Sprintf("wt-%s-%s", runID, taskID)
	wtPath := filepath.Join(wm.worktreesDir, wtName)

	_ = os.RemoveAll(wtPath)

	if wm.isGitRepo {
		// Attempt to create git worktree
		cmd := exec.CommandContext(ctx, "git", "worktree", "add", "--detach", wtPath, "HEAD")
		cmd.Dir = wm.projectRoot
		output, err := cmd.CombinedOutput()
		if err == nil {
			return wtPath, nil
		}
		// Fallback to directory copy if git worktree fails (e.g. detached HEAD or unborn branch)
		_ = output
	}

	// Filesystem isolation fallback
	if err := copyDir(wm.projectRoot, wtPath, []string{".git", ".marshal"}); err != nil {
		return "", fmt.Errorf("failed to create fallback workspace copy: %w", err)
	}

	return wtPath, nil
}

// CleanWorktree removes an isolated worktree when the task finishes.
func (wm *WorktreeManager) CleanWorktree(ctx context.Context, wtPath string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if wtPath == "" || wtPath == wm.projectRoot {
		return nil
	}

	if wm.isGitRepo {
		cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wtPath)
		cmd.Dir = wm.projectRoot
		_ = cmd.Run()
	}

	return os.RemoveAll(wtPath)
}

// ReconcileChanges applies changes from the worktree back to the project root,
// strictly verifying that only permitted scoped files were altered.
func (wm *WorktreeManager) ReconcileChanges(wtPath string, permittedFiles []string) ([]string, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if wtPath == "" || wtPath == wm.projectRoot {
		return nil, nil
	}

	// Collect list of changed files between worktree and projectRoot
	var modified []string

	permittedMap := make(map[string]bool)
	for _, pf := range permittedFiles {
		clean := filepath.Clean(pf)
		if filepath.IsAbs(clean) {
			rel, err := filepath.Rel(wm.projectRoot, clean)
			if err == nil {
				clean = rel
			}
		}
		permittedMap[clean] = true
	}

	err := filepath.Walk(wtPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := info.Name()
		if name == ".git" || name == ".marshal" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(wtPath, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(wm.projectRoot, relPath)
		targetInfo, statErr := os.Stat(targetPath)

		isDifferent := false
		if statErr != nil {
			// New file created
			isDifferent = true
		} else if info.Size() != targetInfo.Size() || info.ModTime() != targetInfo.ModTime() {
			// Compare contents
			wtContent, err1 := os.ReadFile(path)
			tgtContent, err2 := os.ReadFile(targetPath)
			if err1 != nil || err2 != nil || string(wtContent) != string(tgtContent) {
				isDifferent = true
			}
		}

		if isDifferent {
			// Isolation rule: If permittedFiles is specified, reject any mutations outside permitted scope
			if len(permittedFiles) > 0 && !permittedMap[relPath] {
				// Check if directory prefix matches
				matchedDir := false
				for pf := range permittedMap {
					if strings.HasPrefix(relPath, pf+"/") || strings.HasPrefix(pf, relPath+"/") {
						matchedDir = true
						break
					}
				}
				if !matchedDir {
					return fmt.Errorf("%w: task attempted to mutate unpermitted file %s (permitted: %v)",
						ErrIsolationCompromised, relPath, permittedFiles)
				}
			}

			// Copy the modified file to projectRoot
			if err := copyFile(path, targetPath); err != nil {
				return fmt.Errorf("failed to reconcile %s: %w", relPath, err)
			}
			modified = append(modified, relPath)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return modified, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	si, err := os.Stat(src)
	if err == nil {
		_ = os.Chmod(dst, si.Mode())
	}
	return nil
}

func copyDir(src, dst string, skips []string) error {
	skipMap := make(map[string]bool)
	for _, s := range skips {
		skipMap[s] = true
	}

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		parts := strings.Split(rel, string(filepath.Separator))
		for _, p := range parts {
			if skipMap[p] {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		return copyFile(path, target)
	})
}
