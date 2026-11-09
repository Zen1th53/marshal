package worker

import (
	"os"
	"path/filepath"
	"testing"
)

// DefaultBuildDir prefers a per-user cache directory so host builds never fall
// back to a small or quota-limited /tmp, and creates it private to the user.
func TestDefaultBuildDirUsesPerUserCache(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	dir := DefaultBuildDir()
	want := filepath.Join(cache, "marshal", "build")
	if dir != want {
		t.Fatalf("build dir = %q, want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("build dir not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("build dir perm = %o, want 700", perm)
	}
	if dir == os.TempDir() {
		t.Fatalf("build dir fell back to TempDir %q", dir)
	}
}

// With a home but no XDG override, DefaultBuildDir lands under ~/.cache.
func TestDefaultBuildDirFallsBackToHomeCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", home)

	dir := DefaultBuildDir()
	want := filepath.Join(home, ".cache", "marshal", "build")
	if dir != want {
		t.Fatalf("build dir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("build dir not created: %v", err)
	}
}
