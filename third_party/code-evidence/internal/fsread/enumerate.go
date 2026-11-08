package fsread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Mode selects how candidate paths are enumerated.
type Mode string

const (
	// ModeAuto uses git when the root has a .git entry and git is on PATH,
	// otherwise walks the tree.
	ModeAuto Mode = "auto"
	// ModeGit lists tracked plus untracked-but-not-ignored files with a
	// hardened `git ls-files` invocation.
	ModeGit Mode = "git"
	// ModeWalk walks the directory tree through os.Root (no symlinks).
	ModeWalk Mode = "walk"
)

// Filter decides visibility. Denied paths must be dropped here so that
// nothing downstream can depend on them.
type Filter interface {
	Visible(rel string) bool
	DirMayContainVisible(relDir string) bool
}

// Listing is the result of enumeration: visible paths only.
type Listing struct {
	Mode  Mode     // effective mode
	Paths []string // sorted, unique, visible, cleaned
	// Gaps lists visible directories that could not be read (walk mode).
	Gaps []string
}

// Enumerate lists the visible candidate files of the repository.
func (r *Repo) Enumerate(ctx context.Context, mode Mode, f Filter) (Listing, error) {
	if mode == "" {
		mode = ModeAuto
	}
	switch mode {
	case ModeAuto:
		if r.HasGitDir() {
			if _, err := exec.LookPath("git"); err == nil {
				if l, err := r.enumerateGit(ctx, f); err == nil {
					return l, nil
				}
			}
		}
		return r.enumerateWalk(f)
	case ModeGit:
		if !r.HasGitDir() {
			return Listing{}, errors.New("fsread: git mode requested but root has no .git")
		}
		return r.enumerateGit(ctx, f)
	case ModeWalk:
		return r.enumerateWalk(f)
	default:
		return Listing{}, fmt.Errorf("fsread: unknown enumeration mode %q", mode)
	}
}

func finish(mode Mode, paths, gaps []string) Listing {
	slices.Sort(paths)
	paths = slices.Compact(paths)
	slices.Sort(gaps)
	gaps = slices.Compact(gaps)
	return Listing{Mode: mode, Paths: paths, Gaps: gaps}
}

func (r *Repo) enumerateWalk(f Filter) (Listing, error) {
	var paths, gaps []string
	err := fs.WalkDir(r.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if p == "." {
			if err != nil {
				return err
			}
			return nil
		}
		if d != nil && d.IsDir() || err != nil {
			if p == ".git" {
				return fs.SkipDir
			}
			if !f.DirMayContainVisible(p) {
				return fs.SkipDir
			}
			if err != nil {
				// Only report gaps for directories that could hold
				// visible files; denied subtrees were pruned above.
				gaps = append(gaps, p)
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // symlinks, devices, sockets: never followed
		}
		if c, cerr := CleanRel(p); cerr == nil && f.Visible(c) {
			paths = append(paths, c)
		}
		return nil
	})
	if err != nil {
		return Listing{}, err
	}
	return finish(ModeWalk, paths, gaps), nil
}

// gitEnv is the complete environment for git: nothing is inherited except
// PATH, so GIT_* variables, pagers, editors and external diff drivers from
// the caller's environment cannot apply.
func gitEnv(root string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.DevNull,
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CEILING_DIRECTORIES=" + filepath.Dir(root),
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_PAGER=cat",
		"PAGER=cat",
	}
}

// gitArgs hardens a git invocation against repository-controlled config:
// fsmonitor (an arbitrary command), hooks, the untracked cache, external
// diff and pagers are all disabled on the command line, which overrides
// the repository's .git/config.
func gitArgs(sub ...string) []string {
	base := []string{
		"--no-pager",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.untrackedCache=false",
		"-c", "core.pager=cat",
		"-c", "diff.external=",
		"-c", "core.quotePath=false",
	}
	return append(base, sub...)
}

func (r *Repo) enumerateGit(ctx context.Context, f Filter) (Listing, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	// --work-tree pins the work tree to the root: a planted core.worktree
	// could otherwise make git list (and so name) files outside it.
	cmd := exec.CommandContext(ctx, "git", gitArgs("--work-tree="+r.abs, "ls-files", "-z", "--cached", "--others", "--exclude-standard")...)
	cmd.Dir = r.abs
	cmd.Env = gitEnv(r.abs)
	cmd.Stdin = nil
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return Listing{}, fmt.Errorf("fsread: git ls-files: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	var paths []string
	for _, raw := range bytes.Split(out.Bytes(), []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		c, err := CleanRel(string(raw))
		if err != nil || !f.Visible(c) {
			continue
		}
		paths = append(paths, c)
	}
	return finish(ModeGit, paths, nil), nil
}
