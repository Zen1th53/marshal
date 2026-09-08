package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCommitGuardRejectsCredentialPathsAndLeasedSecrets(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "MARSHAL Test")
	git("config", "user.email", "marshal-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "safe.txt"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "safe.txt")
	git("commit", "-q", "-m", "base")

	if err := os.WriteFile(filepath.Join(repo, "auth.json"), []byte(`{"token":"stolen"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureNoSecretsInWorktree(ctx, repo, nil); err == nil {
		t.Fatal("credential-bearing path passed the pre-commit guard")
	}
	if err := os.Remove(filepath.Join(repo, "auth.json")); err != nil {
		t.Fatal(err)
	}
	const leased = "short-secret"
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("copied="+leased), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureNoSecretsInWorktree(ctx, repo, []string{leased}); err == nil {
		t.Fatal("leased secret passed the pre-commit guard")
	}
}

func TestBaselineVerificationCommandAllowlist(t *testing.T) {
	for _, command := range [][]string{
		{"git", "status", "--short"},
		{"go", "test", "./..."},
		{"python", "conformance/runner.py", "validate-pack"},
	} {
		if _, err := resolveBaselineVerificationCommand(command); err != nil {
			t.Fatalf("safe verification rejected %v: %v", command, err)
		}
	}
	for _, command := range [][]string{
		{"sh", "-c", "id"},
		{"git", "push"},
		{"python", "untrusted.py"},
	} {
		if _, err := resolveBaselineVerificationCommand(command); err == nil {
			t.Fatalf("unsafe verification accepted: %v", command)
		}
	}
}
