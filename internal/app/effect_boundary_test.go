package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyConfinesProjectCode(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	outside := filepath.Join(t.TempDir(), "marker")
	if err := os.MkdirAll(filepath.Join(repo.Path(), "conformance"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.Path(), "conformance", "runner.py"), []byte("from pathlib import Path\nPath("+"'"+outside+"'"+").write_text('ran')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Verify(t.Context(), VerifyRequest{}); err == nil {
		t.Fatal("check reached host file")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("check escaped confinement: %v", err)
	}
}

func TestRuntimeCommitDisablesRepositoryHelpers(t *testing.T) {
	for _, helper := range []string{"hook", "filter"} {
		t.Run(helper, func(t *testing.T) {
			repo := runtimeRepo(t)
			root := repo.Path()
			marker := filepath.Join(t.TempDir(), "marker")
			script := filepath.Join(t.TempDir(), "helper")
			if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\ncat\n"), 0755); err != nil {
				t.Fatal(err)
			}
			git := func(args ...string) {
				t.Helper()
				c := exec.Command("git", append([]string{"-C", root}, args...)...)
				if b, err := c.CombinedOutput(); err != nil {
					t.Fatalf("git: %v: %s", err, b)
				}
			}
			if helper == "hook" {
				git("config", "core.hooksPath", filepath.Dir(script))
				if err := os.Rename(script, filepath.Join(filepath.Dir(script), "post-commit")); err != nil {
					t.Fatal(err)
				}
			} else {
				git("config", "filter.delivery.clean", script)
				git("config", "filter.delivery.required", "true")
				if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.txt filter=delivery\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "delivery.txt"), []byte("delivery"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := commitTaskChanges(context.Background(), root, "task"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("repository helper ran: %v", err)
			}
		})
	}
}

func TestIntegrationCheckConfinesCommand(t *testing.T) {
	repo := runtimeRepo(t)
	head, err := gitMarshal(t.Context(), repo.Path(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "marker")
	if err := runIntegrationCheck(t.Context(), repo.Path(), head, "printf ran > '"+marker+"'"); err == nil {
		t.Fatal("accepted host write")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("check escaped: %v", err)
	}
}

func TestRuntimeCommitInspectionDisablesRepositoryHelpers(t *testing.T) {
	repo := runtimeRepo(t)
	marker := filepath.Join(t.TempDir(), "marker")
	script := filepath.Join(t.TempDir(), "monitor")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo.Path(), "config", "core.fsmonitor", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("config: %v %s", err, out)
	}
	if err := ensureNoSecretsInWorktree(t.Context(), repo.Path(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("inspection launched helper: %v", err)
	}
}

func TestProductionMergeRefusesSignatureRequiredHelpers(t *testing.T) {
	for _, format := range []string{"openpgp", "x509", "ssh"} {
		t.Run(format, func(t *testing.T) {
			repo := runtimeRepo(t)
			commit := testgit.SignedCommit(t, repo.Path(), format)
			marker := filepath.Join(t.TempDir(), "marker")
			helper := filepath.Join(t.TempDir(), "verifier")
			if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\nexit 1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{"gpg." + format + ".program": helper, "gpg.format": format, "merge.verifySignatures": "true"} {
				if out, err := exec.Command("git", "-C", repo.Path(), "config", key, value).CombinedOutput(); err != nil {
					t.Fatalf("config: %v %s", err, out)
				}
			}
			if _, err := gitMarshal(t.Context(), repo.Path(), "merge", "--no-ff", "--no-edit", commit); err == nil || !strings.Contains(err.Error(), "required signature verification") {
				t.Fatalf("merge policy was not refused: %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("verifier executed: %v", err)
			}
		})
	}
}
