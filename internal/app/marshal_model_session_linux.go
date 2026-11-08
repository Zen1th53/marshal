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
