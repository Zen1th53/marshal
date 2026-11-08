//go:build linux

package tui

import (
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Zen1th53/marshal/internal/app"
)

func grantPTYRead(t *testing.T, s *ptySession, path string) {
	t.Helper()
	// Keep the exact acknowledgement visible when TMPDIR creates a long path,
	// then restore the test's original viewport for its remaining assertions.
	var size struct{ Row, Col, X, Y uint16 }
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, s.master.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size))); err != 0 {
		t.Fatalf("read terminal size: %v", err)
	}
	if cols := len(path) + 64; cols > int(size.Col) {
		setWinsize(s.master, size.Row, uint16(cols))
		defer setWinsize(s.master, size.Row, size.Col)
	}
	runtime, err := app.Open(t.Context(), s.cmd.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	canonical := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}

	s.sendLine("/permission read allow " + path)
	// Long paths are truncated on screen. Check the exact persisted grant so an
	// earlier command's confirmation cannot satisfy a different path's grant.
	deadline := time.Now().Add(ptyWait(8 * time.Second))
	for time.Now().Before(deadline) {
		grants, err := runtime.OperatorReadGrants(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, grant := range grants {
			if grant.Folder == canonical && grant.Allowed {
				s.mustSee("Read decision recorded:")
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("read grant for %q was not recorded.\n--- output tail ---\n%s", canonical, tail(s.output(), 3000))
}
func approvePTYMemory(t *testing.T, s *ptySession) {
	t.Helper()
	s.sendLine("/memory review")
	approvePTYMemoryListed(t, s)
}
func approvePTYMemoryListed(t *testing.T, s *ptySession) {
	t.Helper()
	s.mustSee("MEM-IMPORT-")
	ids := regexp.MustCompile(`MEM-IMPORT-[0-9a-f]{16}`).FindAllString(s.output(), -1)
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		s.sendLine("/memory allow " + id)
		s.mustSee("Memory decision recorded: " + id)
	}
}
