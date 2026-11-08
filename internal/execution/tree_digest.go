package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/statepath"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WorkspaceTreeDigest binds verification to the bytes Process 05 actually
// leaves behind, including dirty and untracked files. Metadata owned by Git
// and MARSHAL is excluded, as are nested repositories and linked worktrees;
// symlinks are hashed as links and never followed.
func WorkspaceTreeDigest(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	var paths []string
	err = filepath.WalkDir(abs, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		first := strings.Split(filepath.ToSlash(rel), "/")[0]
		if first == ".git" || statepath.Reserved(first) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// A nested repository or linked worktree (its own .git file or
		// directory) belongs to another checkout, not to this workspace.
		if entry.IsDir() {
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return filepath.SkipDir
			}
			// Interpreter and tool caches are rebuilt by any run of the
			// project's own tests (a verifier running them writes
			// __pycache__); they are never part of a delivered result.
			if hostgit.DisposableCacheDir(entry.Name()) {
				return filepath.SkipDir
			}
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, rel := range paths {
		path := filepath.Join(abs, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%s\x00", filepath.ToSlash(rel), info.Mode().String())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			io.WriteString(h, target)
		} else if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return "", err
			}
			opened, err := f.Stat()
			if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
				_ = f.Close()
				return "", fmt.Errorf("workspace entry changed during digest: %s", rel)
			}
			_, copyErr := io.Copy(h, f)
			after, statErr := f.Stat()
			closeErr := f.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if statErr != nil || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
				return "", fmt.Errorf("workspace entry mutated during digest: %s", rel)
			}
			if closeErr != nil {
				return "", closeErr
			}
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
