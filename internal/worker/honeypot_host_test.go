package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestHoneypotHostInspectionDisablesFsmonitor(t *testing.T) {
	repo := testgit.New(t)
	tree := filepath.Join(t.TempDir(), "tree")
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run(repo.Path(), "worktree", "add", "--detach", tree, "HEAD")
	h, err := NewHoneypot(tree)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	meta := filepath.Join(tree, "private-meta")
	if err := os.CopyFS(meta, os.DirFS(filepath.Join(repo.Path(), ".git"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: "+meta+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "host-marker")
	helper := filepath.Join(tree, "fsmonitor")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\nprintf 'token\\000'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	run(tree, "config", "core.worktree", tree)
	run(tree, "config", "core.fsmonitor", helper)
	err = h.Check(t.Context(), nil, nil)
	if err != nil {
		t.Fatalf("clean hand-in rejected: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("host helper ran: %v", err)
	}
}
