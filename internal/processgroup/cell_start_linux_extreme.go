//go:build !governed && linux

package processgroup

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const heldSupervisorArg = "__marshal_held_cell_group"

func init() {
	if len(os.Args) > 3 && os.Args[1] == heldSupervisorArg {
		fd, err := strconv.Atoi(os.Args[2])
		if err != nil || fd < 3 {
			os.Exit(125)
		}
		gate := os.NewFile(uintptr(fd), "private-cell-release")
		var token [1]byte
		_, err = io.ReadFull(gate, token[:])
		_ = gate.Close()
		if err != nil || token[0] != 1 {
			os.Exit(125)
		}
		os.Exit(supervise(os.Args[3:]))
	}
}

// WrapHeld starts only the private supervisor; its sandbox child cannot start
// until release succeeds. Closing without release is a refusal. The caller
// must stop and join the supervisor when its identity callback fails.
func WrapHeld(cmd *exec.Cmd) (release func() error, cleanup func(), err error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	fd := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, read)
	cmd.Args = append([]string{executable, heldSupervisorArg, strconv.Itoa(fd), cmd.Path}, cmd.Args[1:]...)
	cmd.Path = executable
	// A hard owner crash must ask the private subreaper to stop, kill and
	// reap its complete descendant tree, including escaped process groups.
	// Go's Linux fork path also checks for parent death during fork/exec.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	cmd.WaitDelay = time.Second
	if cmd.Cancel != nil {
		cmd.Cancel = func() error { return Stop(cmd) }
	}
	var mu sync.Mutex
	closed := false
	cleanup = func() {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			closed = true
			_ = read.Close()
			_ = write.Close()
		}
	}
	release = func() error {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return fmt.Errorf("private cell release already closed")
		}
		closed = true
		_, err := write.Write([]byte{1})
		_ = read.Close()
		_ = write.Close()
		return err
	}
	return release, cleanup, nil
}
