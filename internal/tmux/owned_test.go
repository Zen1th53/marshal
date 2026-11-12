package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A session NewSession creates is MARSHAL's own; one made any other way is
// not, so a quitting workspace never ends a session the operator made.
func TestNewSessionMarksSessionOwned(t *testing.T) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	sockDir, err := os.MkdirTemp("/tmp", "tmo-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s")
	wrapper := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec "+bin+" -f /dev/null -S "+sock+" \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(wrapper)
	t.Cleanup(func() { exec.Command(bin, "-S", sock, "kill-server").Run(); ResetBinaryPath() })
	ctx := context.Background()

	if err := NewSession(ctx, "marshal-proj-1", t.TempDir(), "marshal", []string{"sleep", "60"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunCommand(ctx, "new-session", "-d", "-s", "marshal-proj-12", "sleep", "60"); err != nil {
		t.Fatal(err)
	}
	if !SessionOwned(ctx, "marshal-proj-1") {
		t.Fatal("the session NewSession created is not marked as MARSHAL's")
	}
	// Exact names: the operator's marshal-proj-12 is not marshal-proj-1's.
	if SessionOwned(ctx, "marshal-proj-12") {
		t.Fatal("a session MARSHAL did not create reads as owned")
	}
	if SessionOwned(ctx, "missing") || SessionOwned(ctx, "") {
		t.Fatal("a missing session reads as owned")
	}
}
