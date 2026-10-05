//go:build linux

package processgroup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Reference binds a supervisor PID to its kernel start time to refuse PID reuse.
type Reference struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

func processIdentity(pid int) (string, bool, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	end := strings.LastIndex(string(data), ")")
	if end < 0 {
		return "", false, fmt.Errorf("invalid process identity")
	}
	fields := strings.Fields(string(data)[end+1:])
	if len(fields) < 20 {
		return "", false, fmt.Errorf("invalid process identity")
	}
	return fields[19], fields[0] != "Z", nil
}
func Identify(cmd *exec.Cmd) (Reference, error) {
	start, alive, err := processIdentity(cmd.Process.Pid)
	if err != nil {
		return Reference{}, err
	}
	if !alive {
		return Reference{}, nil
	}
	return Reference{cmd.Process.Pid, start}, nil
}

// StopReference addresses only the pinned private supervisor. It, rather than
// a terminal host, terminates and reaps the worker's descendants.
func StopReference(ref Reference) error {
	if ref.PID <= 0 || ref.Start == "" {
		return fmt.Errorf("worker supervisor identity missing")
	}
	start, alive, err := processIdentity(ref.PID)
	if err != nil {
		return err
	}
	if !alive {
		return nil
	}
	if start != ref.Start {
		return fmt.Errorf("worker supervisor PID reused: %s", strconv.Itoa(ref.PID))
	}
	if err := syscall.Kill(ref.PID, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		start, alive, err = processIdentity(ref.PID)
		if err != nil {
			return err
		}
		if !alive || start != ref.Start {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("worker supervisor did not finish cleanup")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// RunningReference checks the pinned supervisor without granting authority to
// an unrelated process that reused its PID.
func RunningReference(ref Reference) (bool, error) {
	start, alive, err := processIdentity(ref.PID)
	return alive && start == ref.Start, err
}
