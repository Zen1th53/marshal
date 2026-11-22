//go:build linux

package workerterminal

import (
	"syscall"
	"testing"
)

// The interactive PTY keeps the worker supervisor's parent-death signal.
func assertParentDeathSignal(t *testing.T, attr *syscall.SysProcAttr) {
	t.Helper()
	if attr.Pdeathsig != syscall.SIGKILL {
		t.Fatal("interactive PTY dropped worker parent-death signal")
	}
}
