// Package fsread is the only place that touches a target repository.
//
// Security rules (SPEC §3):
//   - every file read goes through os.Root, so ".." and symlinks cannot
//     escape the repository root;
//   - symlinks and non-regular files are never followed or read;
//   - git, when used, is invoked only as `git ls-files` with a hardened,
//     explicit environment (see git.go); no hooks, filters, fsmonitor,
//     external diff or pager can run;
//   - nothing found in the repository is ever executed.
package fsread

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Repo is a read-only handle on a repository root.
type Repo struct {
	root *os.Root
	abs  string
}

// Open opens dir as a repository root. dir must be a real directory.
func Open(dir string) (*Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("fsread: %s is not a directory", abs)
	}
	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	return &Repo{root: r, abs: abs}, nil
}

// Close releases the root handle.
func (r *Repo) Close() error { return r.root.Close() }

// Abs returns the canonical absolute root path.
func (r *Repo) Abs() string { return r.abs }

// ErrNotRegular is returned for symlinks, directories, devices, etc.
var ErrNotRegular = errors.New("fsread: not a regular file")

// ErrTooLarge is returned when a file exceeds the read cap.
var ErrTooLarge = errors.New("fsread: file exceeds size cap")

// CleanRel validates a repository-relative slash path. It rejects absolute
// paths, "..", empty segments, NUL bytes and anything inside .git.
func CleanRel(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
		return "", fmt.Errorf("fsread: invalid path %q", p)
	}
	c := path.Clean(p)
	if c != p || c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("fsread: invalid path %q", p)
	}
	if c == ".git" || strings.HasPrefix(c, ".git/") {
		return "", fmt.Errorf("fsread: invalid path %q", p)
	}
	return c, nil
}

// ReadFile reads a regular file (not a symlink) of at most max bytes.
func (r *Repo) ReadFile(rel string, max int64) ([]byte, error) {
	b, _, _, err := r.ReadFileKeyed(rel, max)
	return b, err
}

// Lstat stats rel without following a final symlink.
func (r *Repo) Lstat(rel string) (fs.FileInfo, error) {
	rel, err := CleanRel(rel)
	if err != nil {
		return nil, err
	}
	return r.root.Lstat(filepath.FromSlash(rel))
}

// HasGitDir reports whether the root contains a .git entry (dir or file).
func (r *Repo) HasGitDir() bool {
	_, err := r.root.Lstat(".git")
	return err == nil
}

// FS returns a read-only fs.FS confined to the root.
func (r *Repo) FS() fs.FS { return r.root.FS() }
