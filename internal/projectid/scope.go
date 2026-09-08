package projectid

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file resolves the working scope — the part of the filesystem MARSHAL
// may change — and qualifies whether the repository can be worked in safely.
//
// The three concepts the pack insists on keeping apart are kept apart here:
//
//	Repository    what Git tracks
//	Project       what MARSHAL has adopted an identity for
//	WorkingScope  what MARSHAL is allowed to modify
//
// They coincide in the common case and diverge in exactly the situations that
// cause damage: a monorepo where work should touch one package, a checkout
// containing a nested repository whose history MARSHAL does not control, and a
// symlink pointing out of the tree.

// Scope is the resolved boundary of what may be modified.
type Scope struct {
	// Root is the canonical, symlink-resolved project root.
	Root string `json:"root"`
	// Subpath narrows the scope within the root. Empty means the whole root.
	Subpath string `json:"subpath,omitempty"`
	// Excluded are paths inside the root that are outside MARSHAL's control:
	// nested repositories and submodules, whose history belongs to another
	// repository.
	Excluded []string `json:"excluded,omitempty"`
}

// Base returns the directory the scope is anchored at.
func (s Scope) Base() string {
	if s.Subpath == "" {
		return s.Root
	}
	return filepath.Join(s.Root, s.Subpath)
}

// Contains reports whether a path lies inside the scope.
//
// The check resolves symlinks first, so a link inside the project pointing
// outside it does not smuggle a path past the boundary. A path that cannot be
// resolved is refused rather than assumed safe.
func (s Scope) Contains(path string) bool {
	resolved, err := resolvePath(path)
	if err != nil {
		return false
	}
	base, err := resolvePath(s.Base())
	if err != nil {
		return false
	}
	if !withinDir(base, resolved) {
		return false
	}
	// A path inside an excluded subtree is outside the scope even though it is
	// inside the root.
	for _, excluded := range s.Excluded {
		excludedPath, err := resolvePath(filepath.Join(s.Root, excluded))
		if err != nil {
			continue
		}
		if withinDir(excludedPath, resolved) {
			return false
		}
	}
	return true
}

// Violations returns the supplied paths that fall outside the scope, so a
// caller can report exactly which ones are the problem rather than a single
// unhelpful refusal.
func (s Scope) Violations(paths []string) []string {
	var violations []string
	for _, path := range paths {
		if !s.Contains(path) {
			violations = append(violations, path)
		}
	}
	sort.Strings(violations)
	return violations
}

// resolvePath canonicalizes a path, following symlinks. When the path does not
// exist yet, the nearest existing ancestor is resolved instead, so that a file
// about to be created is judged by the directory it would land in.
func resolvePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved, nil
	}
	// Walk up to the nearest existing ancestor and resolve that, re-appending
	// the remainder. This is what stops a not-yet-created path under a
	// symlinked directory from escaping the check.
	remainder := ""
	current := absolute
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return absolute, nil
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(resolved, remainder), nil
		}
	}
}

// withinDir reports whether target is dir or lies beneath it. It compares path
// segments rather than string prefixes, so "/proj-evil" is not treated as
// being inside "/proj".
func withinDir(dir, target string) bool {
	if dir == target {
		return true
	}
	relative, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// GitState qualifies whether a repository can be worked in safely.
type GitState string

const (
	// GitReady means diff, checkpoint and rollback are all available.
	GitReady GitState = "READY"
	// GitLimited means the repository works but recovery is weaker than usual:
	// no commits to compare against, or a detached HEAD.
	GitLimited GitState = "LIMITED"
	// GitBlocked means work cannot be made recoverable here.
	GitBlocked GitState = "BLOCKED"
)

// Recoverable reports whether MARSHAL can undo what it does. Only READY
// qualifies: LIMITED means something a rollback would depend on is missing.
func (s GitState) Recoverable() bool { return s == GitReady }

// GitQualification is the result of inspecting repository state.
type GitQualification struct {
	State GitState `json:"state"`
	// Reason is user-safe.
	Reason string `json:"reason"`
	// Dirty reports uncommitted changes in the working tree.
	Dirty bool `json:"dirty"`
	// DirtyPaths names the changed files, so a user can see what would be at
	// risk rather than being told only that "something" is uncommitted.
	DirtyPaths []string `json:"dirty_paths,omitempty"`
	// DetachedHEAD reports that HEAD does not point at a branch.
	DetachedHEAD bool `json:"detached_head"`
	// HasCommits reports whether there is any history.
	HasCommits bool `json:"has_commits"`
	// NestedRepositories are repositories inside the working tree whose
	// history MARSHAL does not control.
	NestedRepositories []string `json:"nested_repositories,omitempty"`
	// Submodules are declared submodules, likewise outside MARSHAL's control.
	Submodules []string `json:"submodules,omitempty"`
	// MidOperation reports an in-progress merge, rebase, bisect or cherry-pick.
	MidOperation string `json:"mid_operation,omitempty"`
}

// SafeToMutate reports whether MARSHAL may modify the working tree without
// risking the user's own uncommitted work.
//
// Dirty is not by itself a refusal — it is a reason to ask. What it must never
// do is proceed silently, because uncommitted work is the one thing in a
// repository that Git cannot get back.
func (q GitQualification) SafeToMutate() bool {
	return q.State.Recoverable() && !q.Dirty && q.MidOperation == ""
}

// QualifyGit inspects a repository's state.
//
// Every branch reports a condition rather than failing, because all of these
// are states a real repository is legitimately in and the caller's job is to
// explain them, not to error out.
func QualifyGit(ctx context.Context, collector Collector, root string) GitQualification {
	if collector == nil {
		collector = NewGitCollector()
	}
	qualification := GitQualification{}

	if _, err := collector.Run(ctx, root, "rev-parse", "--git-dir"); err != nil {
		return GitQualification{
			State:  GitBlocked,
			Reason: "This directory is not part of a Git repository, so changes could not be undone.",
		}
	}

	if _, err := collector.Run(ctx, root, "rev-parse", "HEAD"); err == nil {
		qualification.HasCommits = true
	}
	if out, err := collector.Run(ctx, root, "symbolic-ref", "--quiet", "HEAD"); err != nil || strings.TrimSpace(string(out)) == "" {
		if qualification.HasCommits {
			qualification.DetachedHEAD = true
		}
	}

	// Porcelain status is stable across Git versions and locales, which
	// matters because this feeds a safety decision.
	if out, err := collector.Run(ctx, root, "status", "--porcelain"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.TrimSpace(line) == "" || len(line) <= 3 {
				continue
			}
			path := strings.TrimSpace(line[3:])
			// MARSHAL's own setup files are not the user's uncommitted work.
			// Counting them made a freshly initialized project look like it
			// had work at risk, which would attach a warning to every routine
			// request in a new project and teach people to ignore it.
			if marshalOwnedPath(path) {
				continue
			}
			qualification.Dirty = true
			qualification.DirtyPaths = append(qualification.DirtyPaths, path)
		}
		sort.Strings(qualification.DirtyPaths)
	}

	qualification.MidOperation = detectMidOperation(root)
	qualification.Submodules = detectSubmodules(ctx, collector, root)
	qualification.NestedRepositories = detectNestedRepositories(root, qualification.Submodules)

	switch {
	case qualification.MidOperation != "":
		qualification.State = GitLimited
		qualification.Reason = "This repository is in the middle of a " + qualification.MidOperation + "."
	case !qualification.HasCommits:
		// Without a commit there is no baseline to diff against or return to,
		// so a rollback would have nothing to restore.
		qualification.State = GitLimited
		qualification.Reason = "This repository has no commits yet, so there is nothing to compare changes against."
	case qualification.DetachedHEAD:
		qualification.State = GitLimited
		qualification.Reason = "This repository is not on a branch, so recorded work could be harder to find later."
	default:
		qualification.State = GitReady
		qualification.Reason = "Changes here can be tracked and undone."
	}
	return qualification
}

// marshalOwnedPath reports whether a path is one MARSHAL creates for itself.
//
// These files belong to MARSHAL's setup rather than to the user's work in
// progress, so they must not be reported as changes at risk. Counting them
// made a freshly initialized project look like it had work at risk, which
// would attach a warning to every routine request in a new project and teach
// people to ignore it.
//
// The list is deliberately narrow. Anything not clearly MARSHAL's own is
// treated as the user's, because being wrong in that direction costs a
// redundant warning, while the other direction risks losing someone's work.
func marshalOwnedPath(path string) bool {
	cleaned := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(path)), "./")
	if cleaned == StateDirName || strings.HasPrefix(cleaned, StateDirName+"/") {
		return true
	}
	switch cleaned {
	case "CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml":
		return true
	default:
		return false
	}
}

// detectMidOperation reports an in-progress Git operation. Working in a
// repository mid-rebase risks entangling MARSHAL's changes with a state the
// user is partway through resolving.
func detectMidOperation(root string) string {
	gitDir := filepath.Join(root, ".git")
	for marker, name := range map[string]string{
		"MERGE_HEAD":       "merge",
		"rebase-merge":     "rebase",
		"rebase-apply":     "rebase",
		"CHERRY_PICK_HEAD": "cherry-pick",
		"REVERT_HEAD":      "revert",
		"BISECT_LOG":       "bisect",
	} {
		if _, err := os.Stat(filepath.Join(gitDir, marker)); err == nil {
			return name
		}
	}
	return ""
}

// detectSubmodules lists declared submodules.
func detectSubmodules(ctx context.Context, collector Collector, root string) []string {
	out, err := collector.Run(ctx, root, "config", "--file", ".gitmodules", "--get-regexp", "path")
	if err != nil {
		return nil
	}
	var submodules []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			submodules = append(submodules, fields[1])
		}
	}
	sort.Strings(submodules)
	return submodules
}

// detectNestedRepositories finds repositories inside the working tree that are
// not declared submodules.
//
// These are the dangerous case: a checkout that happens to contain another
// repository, whose history the outer repository does not track. Changes there
// would not appear in the outer repository's diff and could not be rolled back
// with it, so they are excluded from scope rather than quietly included.
//
// The walk is depth-bounded because a deep tree should not make opening a
// project slow, and skips the outer repository's own metadata.
func detectNestedRepositories(root string, submodules []string) []string {
	declared := make(map[string]bool, len(submodules))
	for _, submodule := range submodules {
		declared[filepath.Clean(submodule)] = true
	}

	var nested []string
	const maxDepth = 4
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > maxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") && entry.Name() != ".git" {
				continue
			}
			child := filepath.Join(dir, entry.Name())
			if entry.Name() == ".git" {
				if child == filepath.Join(root, ".git") {
					continue
				}
				relative, err := filepath.Rel(root, filepath.Dir(child))
				if err == nil && !declared[filepath.Clean(relative)] {
					nested = append(nested, relative)
				}
				continue
			}
			walk(child, depth+1)
		}
	}
	walk(root, 0)
	sort.Strings(nested)
	return nested
}

// ResolveScope determines what MARSHAL may modify.
//
// Nested repositories and submodules are excluded automatically. A requested
// subpath is honoured only when it genuinely lies within the root, so a
// caller cannot widen scope by asking for one that points elsewhere.
func ResolveScope(root, subpath string, qualification GitQualification) (Scope, error) {
	canonicalRoot, err := resolvePath(root)
	if err != nil {
		return Scope{}, err
	}
	scope := Scope{Root: canonicalRoot}
	scope.Excluded = append(scope.Excluded, qualification.NestedRepositories...)
	scope.Excluded = append(scope.Excluded, qualification.Submodules...)
	sort.Strings(scope.Excluded)

	if strings.TrimSpace(subpath) == "" {
		return scope, nil
	}
	// An absolute subpath is refused rather than joined. filepath.Join would
	// quietly turn "/etc" into "<root>/etc", so a caller asking for a path
	// outside the project would get a different path than the one they named
	// and no indication that anything was reinterpreted.
	if filepath.IsAbs(subpath) {
		return Scope{}, ErrScopeEscape
	}
	// The requested subpath must resolve inside the root. Checking after
	// resolution is what stops "../.." or a symlinked directory from widening
	// the scope rather than narrowing it.
	candidate, err := resolvePath(filepath.Join(canonicalRoot, subpath))
	if err != nil {
		return Scope{}, err
	}
	if !withinDir(canonicalRoot, candidate) {
		return Scope{}, ErrScopeEscape
	}
	relative, err := filepath.Rel(canonicalRoot, candidate)
	if err != nil {
		return Scope{}, err
	}
	scope.Subpath = relative
	return scope, nil
}
