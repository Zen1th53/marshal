package testgit

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type Repository struct {
	path string
}

func New(t testing.TB) *Repository {
	t.Helper()

	path := t.TempDir()
	run(t, path, "git", "init", "-b", "main")
	run(t, path, "git", "config", "user.name", "MARSHAL Test")
	run(t, path, "git", "config", "user.email", "marshal-test@example.invalid")
	run(t, path, "git", "commit", "--allow-empty", "-m", "initial")
	return &Repository{path: path}
}

func (r *Repository) Path() string {
	return r.path
}

func (r *Repository) HEAD(t testing.TB) string {
	t.Helper()
	return run(t, r.path, "git", "rev-parse", "HEAD")
}

func run(t testing.TB, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func Canonical(t testing.TB, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}
