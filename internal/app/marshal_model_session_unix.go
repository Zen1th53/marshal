//go:build unix && !linux

package app

import (
	"os/exec"
	"syscall"
)

// detachControllingTerminal starts the provider in a fresh session. Redirecting
// stdin alone would still let it reopen the inherited /dev/tty.
func detachControllingTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// killProviderGroupOnCancel makes a canceled or timed-out turn kill the
// provider's whole session. Setsid makes the provider its own process-group
// leader, so the negative pid reaches every helper it started.
func killProviderGroupOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if cmd.Process.Pid > 1 {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return cmd.Process.Kill()
	}
}
