//go:build linux

package app

import (
	"os/exec"
	"syscall"
)

// detachControllingTerminal starts the provider in a fresh session. Redirecting
// stdin alone would still let it reopen the inherited /dev/tty.
//
// The fresh session also puts the provider out of reach of a kill aimed at
// the runtime's process group, so a runtime (or test binary) that dies
// abruptly would leave it running under init forever; a looping provider
// then burns a CPU indefinitely. Pdeathsig makes the kernel kill it when its
// parent goes away. Pdeathsig is bound to the launching thread; Go keeps
// unlocked threads alive, so it fires only when the runtime itself exits.
func detachControllingTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
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
