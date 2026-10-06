package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestSetupCommitCannotExecuteRepositorySignerOrHook(t *testing.T) {
	for _, kind := range []string{"signer", "hook"} {
		t.Run(kind, func(t *testing.T) {
			repo := testgit.New(t)
			marker := filepath.Join(t.TempDir(), "executed")
			helper := filepath.Join(t.TempDir(), "helper")
			if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\nexit 1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			hooks := t.TempDir()
			if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nprintf ran > '"+marker+"'\nexit 1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			config := map[string]string{"commit.gpgSign": "true", "gpg.program": helper}
			if kind == "hook" {
				config = map[string]string{"core.hooksPath": hooks}
			}
			for key, value := range config {
				if out, err := exec.Command("git", "-C", repo.Path(), "config", key, value).CombinedOutput(); err != nil {
					t.Fatalf("config: %v: %s", err, out)
				}
			}
			err := runGit(t.Context(), repo.Path(), "commit", "--allow-empty", "--only", "-m", "Initial commit")
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("setup executed repository helper: %v (commit: %v)", statErr, err)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}

}
