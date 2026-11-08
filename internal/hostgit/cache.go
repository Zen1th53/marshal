package hostgit

import "strings"

// disposableCacheDirs names directories that only hold caches written by
// running the project's code or tools, at any depth. Any run of the project's
// own tests rebuilds them (`python3 -m unittest` writes __pycache__), and they
// are never part of a delivered result.
var disposableCacheDirs = map[string]bool{"__pycache__": true, ".pytest_cache": true, ".mypy_cache": true, ".ruff_cache": true}

// DisposableCacheDir reports whether a directory name is an interpreter or
// tool cache that is never part of a delivered result.
func DisposableCacheDir(name string) bool { return disposableCacheDirs[name] }

// SourceStatusChanges returns the entries of `git status --porcelain=v1
// --untracked-files=all` (run without --ignored, so files the repository's
// .gitignore excludes are already absent) that change source: every tracked
// change, and every untracked path outside a disposable cache directory. An
// untracked file inside a cache directory is a by-product of running the
// project's code, not a change to it. A quoted (unusual) path is always kept.
func SourceStatusChanges(status string) []string {
	var out []string
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "?? ") && untrackedCachePath(line[3:]) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func untrackedCachePath(path string) bool {
	if path == "" || strings.HasPrefix(path, `"`) {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	// The last element is the file itself (or a whole cache directory when
	// git collapses it); a cache directory anywhere on the path qualifies.
	for _, part := range parts {
		if disposableCacheDirs[part] {
			return true
		}
	}
	return false
}
