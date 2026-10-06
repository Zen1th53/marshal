package hostgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestHostGitDisablesFiltersAndSigning(t *testing.T) {
	for _, key := range []string{"filter.delivery.clean", "filter.delivery.process", "gpg.program"} {
		t.Run(key, func(t *testing.T) {
			repo := testgit.New(t)
			dir := repo.Path()
			marker := filepath.Join(t.TempDir(), "marker")
			script := filepath.Join(t.TempDir(), "helper")
			if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\ncat\n"), 0755); err != nil {
				t.Fatal(err)
			}
			config := func(key, value string) {
				t.Helper()
				cmd := exec.Command("git", "-C", dir, "config", key, value)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("config: %v %s", err, out)
				}
			}
			config(key, script)
			config("filter.delivery.required", "true")
			config("commit.gpgSign", "true")
			if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=delivery\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("delivery"), 0644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "delivery"}} {
				cmd, err := Command(t.Context(), dir, args...)
				if err != nil {
					t.Fatal(err)
				}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git: %v: %s", err, out)
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("Git helper ran: %v", err)
			}
		})
	}
}

func TestHostGitRejectsExternalMergeDrivers(t *testing.T) {
	repo := testgit.New(t)
	cmd := exec.Command("git", "-C", repo.Path(), "config", "merge.delivery.driver", "unused")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("config: %v %s", err, out)
	}
	if _, err := Command(t.Context(), repo.Path(), "merge", "HEAD"); err == nil {
		t.Fatal("accepted external merge driver")
	}
	if _, err := Command(t.Context(), repo.Path(), "merge", "--abort"); err != nil {
		t.Fatal(err)
	}
}

func TestHostGitRejectsSignatureRequiredMerges(t *testing.T) {
	for _, key := range []string{"gpg.program", "gpg.openpgp.program", "gpg.x509.program", "gpg.ssh.program"} {
		t.Run(key, func(t *testing.T) {
			repo := testgit.New(t)
			format := "openpgp"
			if key == "gpg.x509.program" {
				format = "x509"
			}
			if key == "gpg.ssh.program" {
				format = "ssh"
			}
			commit := testgit.SignedCommit(t, repo.Path(), format)
			marker := filepath.Join(t.TempDir(), "marker")
			helper := filepath.Join(t.TempDir(), "verifier")
			if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\nexit 1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			for k, v := range map[string]string{key: helper, "gpg.format": format, "merge.verifySignatures": "true"} {
				if out, err := exec.Command("git", "-C", repo.Path(), "config", k, v).CombinedOutput(); err != nil {
					t.Fatalf("config: %v %s", err, out)
				}
			}
			if cmd, err := Command(t.Context(), repo.Path(), "merge", "--no-ff", "--no-edit", commit); err == nil || cmd != nil || !strings.Contains(err.Error(), "required signature verification") {
				t.Fatalf("signature policy was not refused: %v %v", cmd, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("verifier executed: %v", err)
			}
		})
	}
	repo := testgit.New(t)
	for _, args := range [][]string{{"merge", "--verify-signatures", "HEAD"}, {"merge", "--abort", "--verify-signatures"}} {
		if cmd, err := Command(t.Context(), repo.Path(), args...); err == nil || cmd != nil || !strings.Contains(err.Error(), "required signature verification") {
			t.Fatalf("explicit verification was not refused: %v %v", cmd, err)
		}
	}
}
