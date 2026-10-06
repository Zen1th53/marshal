package testgit

import (
	"os"
	"path/filepath"
	"testing"
)

// RepointedWorktree models a worker replacing its writable .git pointer with
// private metadata while leaving the repository's original metadata intact.
// The private repository selects marker-producing host helpers.
func RepointedWorktree(t testing.TB, format string) (tree, marker string) {
	t.Helper()
	repo := New(t)
	tree = filepath.Join(t.TempDir(), "tree")
	run(t, repo.Path(), "git", "worktree", "add", "--detach", tree, "HEAD")
	metadata := filepath.Join(t.TempDir(), "private-git")
	if err := os.CopyFS(metadata, os.DirFS(filepath.Join(repo.Path(), ".git"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: "+metadata+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marker = filepath.Join(t.TempDir(), "host-helper-ran")
	helper := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	hooks := t.TempDir()
	for _, name := range []string{"pre-commit", "post-commit", "post-merge", "post-checkout"} {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte("#!/bin/sh\nprintf ran > '"+marker+"'\nexit 1\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range map[string]string{
		"core.worktree": tree, "commit.gpgSign": "true", "gpg.format": format,
		"gpg.program": helper, "gpg.openpgp.program": helper, "gpg.x509.program": helper, "gpg.ssh.program": helper,
		"user.signingkey": "dummy", "core.hooksPath": hooks, "core.fsmonitor": helper,
		"filter.host.clean": helper, "filter.host.process": helper, "filter.host.required": "true",
	} {
		run(t, tree, "git", "config", key, value)
	}
	if err := os.WriteFile(filepath.Join(tree, ".gitattributes"), []byte("*.txt filter=host\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "worker.txt"), []byte("worker result\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return tree, marker
}
