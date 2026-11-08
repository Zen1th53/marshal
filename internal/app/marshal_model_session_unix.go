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
