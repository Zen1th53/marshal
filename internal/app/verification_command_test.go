package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestBaselineVerificationDoesNotRunGitHelpers(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo.Path()}, args...)...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	if err := os.WriteFile(filepath.Join(repo.Path(), "tracked.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked.txt")
	git("commit", "-m", "tracked file")
	if err := os.WriteFile(filepath.Join(repo.Path(), "tracked.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Each helper would leave evidence if verification invoked it.
	if err := os.WriteFile(filepath.Join(repo.Path(), "helper"), []byte("#!/bin/sh\nprintf invoked > helper-ran\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	git("config", "diff.external", "./helper")
	git("config", "diff.checkpoint.textconv", "./helper")
	git("config", "core.fsmonitor", "./helper")
	git("config", "gpg.program", "./helper")
	git("config", "log.showSignature", "true")
	git("config", "merge.checkpoint.driver", "./helper %O %A %B")
	git("config", "log.diffMerges", "remerge")
	if err := os.WriteFile(filepath.Join(repo.Path(), ".gitattributes"), []byte("tracked.txt diff=checkpoint merge=checkpoint\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("checkout", "-b", "side")
	git("commit", "-am", "side change")
	git("checkout", "main")
	if err := os.WriteFile(filepath.Join(repo.Path(), "tracked.txt"), []byte("main change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "-am", "main change")
	git("merge", "side", "--no-edit")
	header, body, ok := strings.Cut(git("cat-file", "commit", "HEAD"), "\n\n")
	if !ok {
		t.Fatal("commit has no message")
	}
	cmd := exec.Command("git", "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Dir = repo.Path()
	cmd.Stdin = strings.NewReader(header + "\ngpgsig -----BEGIN PGP SIGNATURE-----\n invalid\n -----END PGP SIGNATURE-----\n\n" + body + "\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("create signed commit fixture: %v: %s", err, output)
	}
	git("update-ref", "HEAD", strings.TrimSpace(string(output)))
	if err := os.WriteFile(filepath.Join(repo.Path(), "tracked.txt"), []byte("after merge\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{repo.Path(), filepath.Join(repo.Path(), ".git")} {
		if err := os.Remove(filepath.Join(dir, "helper-ran")); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	for _, command := range [][]string{
		{"git", "diff"},
		{"git", "diff", "--ext-diff", "--textconv", "--", "tracked.txt"},
		{"git", "show", "--textconv", "HEAD:tracked.txt"},
		{"git", "log", "-p", "--ext-diff", "--textconv", "--", "tracked.txt"},
		{"git", "log", "-m", "--show-signature", "-1"},
		{"git", "show", "--show-signature", "HEAD"},
		{"git", "status", "--short"},
	} {
		t.Run(command[1], func(t *testing.T) {
			_, err := runtime.Verify(ctx, VerifyRequest{Command: command})
			marker := filepath.Join(repo.Path(), "helper-ran")
			gitMarker := filepath.Join(repo.Path(), ".git", "helper-ran")
			for _, path := range []string{marker, gitMarker} {
				if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
					_ = os.Remove(path)
					t.Fatalf("verification invoked a repository helper: %v (marker: %v)", err, statErr)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVerificationPreservesPolicyAuthorizedCommands(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	command := []string{"/bin/sh", "-c", "printf authorized"}
	if _, err := runtime.Verify(ctx, VerifyRequest{Command: command}); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("baseline shell command = %v, want denied", err)
	}
	p := policy.Policy{ID: "verification", Version: 1, Default: policy.EffectDeny,
		Rules: []policy.Rule{{ID: "shell", Description: "allow verification shell", When: map[string]string{"action": "verify", "resource": "/bin/sh"}, Effect: policy.EffectAllow}}}
	if err := runtime.store.PutPolicy(ctx, store.PolicyRecord{Policy: p,
		Binding: policy.PolicyBinding{Version: 1, Digest: mustPolicyDigest(t, p), Generation: 1}, State: policy.StateActive}); err != nil {
		t.Fatal(err)
	}
	runtime.policyConfigured = true
	runtime.runtimePolicy = RuntimePolicyConfig{PolicyID: p.ID, PolicyVersion: p.Version}
	result, err := runtime.Verify(ctx, VerifyRequest{Command: command})
	if err != nil || result.Stdout != "authorized" {
		t.Fatalf("authorized shell command = %+v, %v", result, err)
	}
	if _, err := runtime.Verify(ctx, VerifyRequest{Command: []string{"/bin/echo", "unauthorized"}}); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("unlisted executable = %v, want denied", err)
	}
}
