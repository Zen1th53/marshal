package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketPathStaysInStateDirWhenShort(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "p-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	got := SocketPath(root)
	if filepath.Base(got) != "runtime.sock" || !strings.HasPrefix(got, root) {
		t.Fatalf("short root should keep the in-project socket, got %q", got)
	}
}

func TestSocketPathFallsBackWhenTooLong(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	root := filepath.Join(t.TempDir(), strings.Repeat("deep-project-name-", 8))
	got := SocketPath(root)
	if len(got) > maxSocketPath {
		t.Fatalf("socket path %q is %d bytes, over %d", got, len(got), maxSocketPath)
	}
	if strings.HasPrefix(got, root) {
		t.Fatalf("fallback socket must live outside the project, got %q", got)
	}
	if got != SocketPath(root) {
		t.Fatal("socket path must be deterministic so clients can discover it")
	}
	if other := SocketPath(root + "x"); other == got {
		t.Fatal("different projects must not share a socket")
	}
	dir := filepath.Dir(got)
	if err := ensureShortSocketDir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(dir) })
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("fallback directory mode: %v %v", info, err)
	}
}
