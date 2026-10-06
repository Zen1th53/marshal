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

// SignedCommit creates a descendant with a signature header. Its dummy signature
// is sufficient to exercise Git's verifier dispatch without signing credentials.
func SignedCommit(t testing.TB, dir, format string) string {
	t.Helper()
	label := map[string]string{"openpgp": "PGP SIGNATURE", "x509": "SIGNED MESSAGE", "ssh": "SSH SIGNATURE"}[format]
	if label == "" {
		t.Fatalf("unknown signature format %q", format)
	}
	tree := run(t, dir, "git", "rev-parse", "HEAD^{tree}")
	parent := run(t, dir, "git", "rev-parse", "HEAD")
	content := "tree " + tree + "\nparent " + parent + "\nauthor Test <test@example.invalid> 1700000000 +0000\ncommitter Test <test@example.invalid> 1700000000 +0000\ngpgsig -----BEGIN " + label + "-----\n dummy\n -----END " + label + "-----\n\nsigned descendant\n"
	cmd := exec.Command("git", "-C", dir, "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(content)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("write signature-bearing commit: %v: %s", err, output)
	}
	commit := strings.TrimSpace(string(output))
	if got := run(t, dir, "git", "cat-file", "commit", commit); !strings.Contains(got, "gpgsig -----BEGIN "+label) {
		t.Fatal("missing signature header")
	}
	return commit
}
