// Package statepath names project-local state and preserves the former engine
// namespace during the public upgrade. It never merges existing directories.
package statepath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func Path(root string, parts ...string) string {
	return filepath.Join(append([]string{root, DirName}, parts...)...)
}

// Reserved protects both variants even when only one binary is running.
func Reserved(name string) bool {
	name = filepath.ToSlash(filepath.Clean(name))
	for _, dir := range []string{DirName, OtherDirName} {
		if name == dir || strings.HasPrefix(name, dir+"/") {
			return true
		}
	}
	return false
}

// Validate refuses pre-existing aliases before project state is consumed.
// Checked-out files under worktrees/branches/checkpoints can contain symlinks;
// their container directories themselves must still belong to this namespace.
// This is entrypoint validation, not a substitute for OS sandbox confinement.
func Validate(root string) error {
	if _, err := os.Lstat(Path(root, migrationMarker)); err == nil {
		return fmt.Errorf("Marshal state migration is pending; reopen the project to complete it")
	} else if !os.IsNotExist(err) {
		return err
	}
	return validateNamespace(root, DirName, OtherDirName)
}

func validateNamespace(root, dir, otherDir string) error {
	state := filepath.Join(root, dir)
	info, err := os.Lstat(state)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state namespace is not a real directory: %s", DirName)
	}
	if other, err := os.Stat(filepath.Join(root, otherDir)); otherDir != "" && err == nil && os.SameFile(info, other) {
		return fmt.Errorf("state directory is shared with another variant")
	}
	return filepath.WalkDir(state, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return vanished(walkErr, path != state)
		}
		rel, err := filepath.Rel(state, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("state namespace contains an aliased operational path: %s", rel)
		}
		if rel == "worktrees" || rel == "branches" || rel == "checkpoints" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		// A regular file hard-linked to the other variant is also shared state.
		here, err := entry.Info()
		if err != nil {
			return vanished(err, true)
		}
		if multipleLinks(here) && !strings.HasPrefix(filepath.ToSlash(rel), "artifacts/") {
			return fmt.Errorf("operational state file has multiple hard links: %s", rel)
		}
		there, err := os.Stat(filepath.Join(root, otherDir, rel))
		if err == nil && os.SameFile(here, there) {
			return fmt.Errorf("state file is shared with another variant: %s", rel)
		}
		return nil
	})
}

// ValidateConfig checks existing installation-state aliases before identity reads.
func ValidateConfig(base string) error {
	dir := filepath.Join(base, Name)
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("config namespace is not a real directory: %s", Name)
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		return nil
	}
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return vanished(err, path != dir)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("config namespace contains an aliased operational path: %s", rel)
		}
		if entry.IsDir() {
			return nil
		}
		here, err := entry.Info()
		if err != nil {
			return vanished(err, true)
		}
		if multipleLinks(here) {
			return fmt.Errorf("config file has multiple hard links: %s", rel)
		}
		there, err := os.Stat(filepath.Join(base, strings.TrimPrefix(OtherDirName, "."), rel))
		if err == nil && os.SameFile(here, there) {
			return fmt.Errorf("config file is shared with another variant: %s", rel)
		}
		return nil
	})
}

// vanished ignores an entry removed by a concurrent writer (SQLite journals,
// atomic-rename temporaries, lock files) between listing and inspection. A
// path that no longer exists cannot alias another variant's state; the walk
// root itself and every other error still refuse.
func vanished(err error, entry bool) error {
	if entry && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// CheckPath validates only a single operational path and its existing parents.
// It is cheap enough for native-state read/write callbacks after startup.
func CheckPath(root, path string) error {
	state := Path(root)
	rel, err := filepath.Rel(state, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("operational path belongs to a different namespace")
	}
	parts := strings.Split(rel, string(filepath.Separator))
	current := state
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			current = filepath.Join(current, parts[i])
		}
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("aliased operational state path refused")
		}
		if i == -1 {
			if other, err := os.Stat(filepath.Join(root, OtherDirName)); err == nil && os.SameFile(info, other) {
				return fmt.Errorf("state directory is shared with another variant")
			}
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("operational state parent is not a directory")
		}
		if i == len(parts)-1 && !info.IsDir() {
			if multipleLinks(info) && !strings.HasPrefix(filepath.ToSlash(rel), "artifacts/") {
				return fmt.Errorf("operational state file has multiple hard links")
			}
			other, err := os.Stat(filepath.Join(root, OtherDirName, rel))
			if err == nil && os.SameFile(info, other) {
				return fmt.Errorf("operational file is shared with another variant")
			}
		}
	}
	return nil
}
