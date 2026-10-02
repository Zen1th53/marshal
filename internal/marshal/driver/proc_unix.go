//go:build unix

package driver

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the command in its own process group and makes
// cancellation kill the whole group. A worker CLI starts children of its
// own; killing only the parent would leave them editing the worktree.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
