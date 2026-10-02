//go:build darwin

package store

import (
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
	"golang.org/x/sys/unix"
)

func supervisorProcessStamp(pid int) (string, error) {
	if pid <= 0 {
		return "", model.ErrInvalid
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("process identity unavailable: %w", err)
	}
	start := info.Proc.P_starttime
	if info.Proc.P_pid != int32(pid) || start.Sec <= 0 || start.Usec < 0 || start.Usec >= 1000000 {
		return "", fmt.Errorf("process identity unavailable: invalid kernel start time")
	}
	return fmt.Sprintf("%d.%06d", start.Sec, start.Usec), nil
}
