package project

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"io/fs"
	"os"
	"path/filepath"
)

// Repair Git's reciprocal links, which live in the repository metadata outside
// state. Project files in worktrees and checkpoints are never rewritten.
func repairWorktrees(root, state string) error {
	for _, container := range []string{"worktrees", "branches"} {
		base := filepath.Join(state, container)
		if _, err := os.Stat(base); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.Name() != ".git" {
				return nil
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			cmd, err := hostgit.Command(context.Background(), root, "worktree", "repair", filepath.Dir(path))
			if err != nil {
				return err
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("repair moved worktree: %w: %s", err, out)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
