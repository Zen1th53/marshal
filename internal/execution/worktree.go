package execution

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	branchworktree "github.com/Zen1th53/marshal/internal/worktree"
)

func (e *Engine) branchManager() *branchworktree.Manager {
	return branchworktree.New(e.cfg.ProjectRoot, filepath.Join(e.cfg.ProjectRoot, ".marshal", "branches"))
}

func branchTaskID(taskID string) string {
	if strings.HasPrefix(taskID, "TASK-") {
		return taskID
	}
	return "TASK-" + taskID
}

func (e *Engine) prepareTaskWorktree(ctx context.Context, run ExecutionRun, task TaskExecution) (string, error) {
	if run.Delivery != DeliveryPreserveBranch {
		return e.worktrees.PrepareWorktree(ctx, task.TaskID, run.RunID)
	}
	baseCommit := run.BaseCommit
	if task.BaseCommit != "" {
		baseCommit = task.BaseCommit
	}
	request := model.WorktreeRequest{TaskID: branchTaskID(task.TaskID), Branch: "marshal/" + run.RunID + "/" + task.TaskID, BaseCommit: baseCommit}
	if task.NativeTurn != nil {
		// The live provider may already have changed its worktree after approval.
		// Reuse only the exact persisted path, branch, and base commit; Prepare
		// intentionally rejects dirty worktrees for every other admission.
		expected := filepath.Join(e.cfg.ProjectRoot, ".marshal", "branches", request.TaskID)
		expectedAbs, err := filepath.Abs(expected)
		if err != nil {
			return "", err
		}
		boundAbs, err := filepath.Abs(task.WorktreePath)
		if err != nil {
			return "", err
		}
		nativeAbs, err := filepath.Abs(task.NativeTurn.Worktree)
		if err != nil {
			return "", err
		}
		if task.WorktreePath == "" || task.NativeTurn.Worktree == "" || boundAbs != expectedAbs || nativeAbs != expectedAbs || task.ResultCommit != "" {
			return "", fmt.Errorf("%w: native turn worktree binding differs", model.ErrConflict)
		}
		state, err := e.branchManager().Inspect(ctx, expectedAbs)
		if err != nil {
			return "", err
		}
		if state.Path != expectedAbs || state.Branch != request.Branch || state.HEAD != request.BaseCommit {
			return "", fmt.Errorf("%w: native turn worktree identity differs", model.ErrConflict)
		}
		return state.Path, nil
	}
	if task.ResultCommit != "" {
		request.BaseCommit = task.ResultCommit
		wt, err := e.branchManager().Resume(ctx, request)
		return wt.Path, err
	}
	wt, err := e.branchManager().Prepare(ctx, request)
	return wt.Path, err
}

func commitTaskWorktree(ctx context.Context, path, runID, taskID string) (string, error) {
	git := func(args ...string) (string, error) {
		cmd, err := hostgit.Command(ctx, path, args...)
		if err != nil {
			return "", err
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimSpace(string(out)), nil
	}
	status, err := git("status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return "", err
	}
	if status != "" {
		if _, err := git("add", "-A"); err != nil {
			return "", err
		}
		for _, key := range []string{"user.name", "user.email"} {
			value, err := git("config", "--get", key)
			if err != nil || value == "" {
				return "", fmt.Errorf("git commit requires %s; configure it in the repository or git configuration", key)
			}
		}
		if _, err := git("commit", "--no-verify", "-m", fmt.Sprintf("marshal: hand-in for %s (run %s)", taskID, runID)); err != nil {
			return "", err
		}
	}
	return git("rev-parse", "HEAD")
}

// WorktreeManager manages isolated working directories and git worktrees for tasks.
type WorktreeManager struct {
	mu            sync.Mutex
	projectRoot   string
	worktreesDir  string
	isGitRepo     bool
	projectInfo   os.FileInfo
	worktreesInfo os.FileInfo
	owned         map[string]os.FileInfo
	workspaces    map[string]string
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

	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	projectInfo, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	trees, err := openDirectory(root, filepath.Join(".marshal", "worktrees"), true, nil)
	if err != nil {
		return nil, err
	}
	defer trees.Close()
	treesInfo, err := trees.Stat(".")
	if err != nil {
		return nil, err
	}
	cmd, err := hostgit.Command(context.Background(), absRoot, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return nil, err
	}
	return &WorktreeManager{projectRoot: absRoot, worktreesDir: filepath.Join(absRoot, ".marshal", "worktrees"), isGitRepo: cmd.Run() == nil, projectInfo: projectInfo, worktreesInfo: treesInfo, owned: make(map[string]os.FileInfo), workspaces: make(map[string]string)}, nil
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

	if !plan.SafeTaskID(taskID) || !plan.SafeTaskID(runID) {
		return "", fmt.Errorf("%w: unsafe workspace identifier", ErrIsolationCompromised)
	}
	root, err := wm.openWorktrees()
	if err != nil {
		return "", err
	}
	defer root.Close()
	key := runID + "\x00" + taskID
	if path, ok := wm.workspaces[key]; ok {
		info, err := root.Lstat(filepath.Base(path))
		if err != nil || !info.IsDir() || !os.SameFile(wm.owned[path], info) {
			return "", fmt.Errorf("%w: workspace identity changed", ErrIsolationCompromised)
		}
		return path, nil
	}
	wtName, err := model.NewID("wt-")
	if err != nil {
		return "", err
	}
	if err := root.Mkdir(wtName, 0700); err != nil {
		return "", err
	}
	info, err := root.Lstat(wtName)
	if err != nil {
		return "", err
	}
	wtPath := filepath.Join(wm.worktreesDir, wtName)
	wm.owned[wtPath] = info
	gitCheckout := false
	if wm.isGitRepo {
		cmd, err := hostgit.Command(ctx, wm.projectRoot, "worktree", "add", "--detach", wtPath, "HEAD")
		if err != nil {
			return "", err
		}
		_, gitErr := cmd.CombinedOutput()
		gitCheckout = gitErr == nil
	}
	source, err := os.OpenRoot(wm.projectRoot)
	if err != nil {
		return "", err
	}
	defer source.Close()
	sourceInfo, err := source.Stat(".")
	if err != nil || !os.SameFile(wm.projectInfo, sourceInfo) {
		return "", fmt.Errorf("%w: project root changed", ErrIsolationCompromised)
	}
	target, err := openDirectory(root, wtName, false, nil)
	if err != nil {
		return "", err
	}
	defer target.Close()
	if gitCheckout {
		// Git records only executable bits. Preserve project permissions on
		// checked-out files without importing untracked files or project bytes.
		if err := preserveCheckoutModes(source, target); err != nil {
			return "", err
		}
	} else if _, err := reconcileFiles(source, target, nil, []string{".git", ".marshal"}); err != nil {
		return "", fmt.Errorf("failed to create fallback workspace copy: %w", err)
	}
	wm.workspaces[key] = wtPath
	return wtPath, nil
}

func (wm *WorktreeManager) openWorktrees() (*os.Root, error) {
	root, err := os.OpenRoot(wm.projectRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil || !os.SameFile(wm.projectInfo, info) {
		return nil, fmt.Errorf("%w: project root changed", ErrIsolationCompromised)
	}
	trees, err := openDirectory(root, filepath.Join(".marshal", "worktrees"), false, nil)
	if err != nil {
		return nil, err
	}
	info, err = trees.Stat(".")
	if err != nil || !os.SameFile(wm.worktreesInfo, info) {
		trees.Close()
		return nil, fmt.Errorf("%w: worktree root changed", ErrIsolationCompromised)
	}
	return trees, nil
}

// CleanWorktree removes only a workspace created and recorded by this manager.
func (wm *WorktreeManager) CleanWorktree(ctx context.Context, wtPath string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	if wtPath == "" {
		return nil
	}
	root, err := wm.openWorktrees()
	if err != nil {
		return err
	}
	defer root.Close()
	recorded, ok := wm.owned[wtPath]
	name, err := filepath.Rel(wm.worktreesDir, wtPath)
	if !ok || err != nil || !safeRelative(name) || filepath.Base(name) != name {
		return fmt.Errorf("%w: workspace is not owned by this runtime", ErrIsolationCompromised)
	}
	info, err := root.Lstat(name)
	if err != nil || !info.IsDir() || !os.SameFile(recorded, info) {
		return fmt.Errorf("%w: workspace identity changed", ErrIsolationCompromised)
	}
	resolved, err := filepath.EvalSymlinks(wtPath)
	if err != nil || resolved != wtPath {
		return fmt.Errorf("%w: workspace containment changed", ErrIsolationCompromised)
	}
	if err := root.RemoveAll(name); err != nil {
		return err
	}
	delete(wm.owned, wtPath)
	for key, path := range wm.workspaces {
		if path == wtPath {
			delete(wm.workspaces, key)
		}
	}
	// Prune the registration after rooted removal; Git never receives a deletion path.
	if wm.isGitRepo {
		cmd, err := hostgit.Command(ctx, wm.projectRoot, "worktree", "prune", "--expire=now")
		if err != nil {
			return err
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("prune worktree registration: %w: %s", err, out)
		}
	}
	return nil
}

// ReconcileChanges applies changes from the worktree back to the project root,
// strictly verifying that only permitted scoped files were altered.
func (wm *WorktreeManager) ReconcileChanges(wtPath string, permittedFiles []string) ([]string, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	trees, err := wm.openWorktrees()
	if err != nil {
		return nil, err
	}
	defer trees.Close()
	recorded, ok := wm.owned[wtPath]
	name, err := filepath.Rel(wm.worktreesDir, wtPath)
	if !ok || err != nil || !safeRelative(name) || filepath.Base(name) != name {
		return nil, fmt.Errorf("%w: unowned delivery workspace", ErrIsolationCompromised)
	}
	source, err := openDirectory(trees, name, false, nil)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	info, err := source.Stat(".")
	if err != nil || !os.SameFile(recorded, info) {
		return nil, fmt.Errorf("%w: delivery workspace changed", ErrIsolationCompromised)
	}
	target, err := os.OpenRoot(wm.projectRoot)
	if err != nil {
		return nil, err
	}
	defer target.Close()
	targetInfo, err := target.Stat(".")
	if err != nil || !os.SameFile(wm.projectInfo, targetInfo) {
		return nil, fmt.Errorf("%w: project root changed", ErrIsolationCompromised)
	}
	return reconcileFiles(source, target, permittedFiles, []string{".git", ".marshal"})
}

func copyDir(src, dst string, skips []string) error {
	source, err := os.OpenRoot(src)
	if err != nil {
		return err
	}
	defer source.Close()
	parent, err := os.OpenRoot(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer parent.Close()
	if _, err := parent.Lstat(filepath.Base(dst)); os.IsNotExist(err) {
		if err := parent.Mkdir(filepath.Base(dst), 0755); err != nil {
			return err
		}
	}
	target, err := openDirectory(parent, filepath.Base(dst), false, nil)
	if err != nil {
		return err
	}
	defer target.Close()
	_, err = reconcileFiles(source, target, nil, skips)
	return err
}

func preserveCheckoutModes(source, target *os.Root) error {
	return fs.WalkDir(target.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == ".git" || name == ".marshal" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := source.Lstat(name)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return target.Chmod(name, info.Mode().Perm())
	})
}
