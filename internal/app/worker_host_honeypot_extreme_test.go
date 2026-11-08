//go:build !governed && linux

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// A host worker's worktree gets the honeypot scan identity that completion,
// provider-failure failover and hand-in require, without writing into it.
func TestExtremeHostWorkerArmsHoneypotScanIdentity(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\nprintf '{}\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := &Runtime{}
	worktree := t.TempDir()
	task := model.Task{ID: "TASK-host-honeypot", Title: "host worker", Risk: model.R2}
	if _, _, err := r.resolveAdapter(t.Context(), "codex", task, worktree, "worker", true, "", ""); err != nil {
		t.Fatal(err)
	}
	r.honeypotMu.Lock()
	trap := r.honeypots[worktree]
	r.honeypotMu.Unlock()
	if trap == nil || trap.Worktree() != worktree {
		t.Fatal("host worker worktree has no honeypot scan identity")
	}
	if !strings.HasPrefix(trap.Home, "/tmp/") || strings.HasPrefix(trap.Home, worktree) {
		t.Fatalf("honeypot home %q is not private scratch storage", trap.Home)
	}
	entries, err := os.ReadDir(worktree)
	if err != nil || len(entries) != 0 {
		t.Fatalf("honeypot wrote into the worktree: %v %v", entries, err)
	}
	// The same worktree reuses its identity.
	if again, err := r.armHoneypot(worktree); err != nil || again != trap {
		t.Fatal("honeypot identity not stable for its worktree")
	}
	_ = os.RemoveAll(filepath.Dir(trap.Home))
}
