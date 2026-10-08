//go:build linux

// Package processgroup owns the lifetime of a worker's descendant processes.
package processgroup

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const supervisorArg = "__marshal_process_group"

func init() {
	if len(os.Args) > 2 && os.Args[1] == supervisorArg {
		os.Exit(supervise(os.Args[2:]))
	}
}

// Wrap inserts a private subreaper outside the worker/sandbox. Unlike killing
// a group alone, this also owns descendants that call setsid or double-fork.
func Wrap(cmd *exec.Cmd) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd.Args = append([]string{executable, supervisorArg, cmd.Path}, cmd.Args[1:]...)
	cmd.Path = executable
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	if cmd.Cancel != nil {
		cmd.Cancel = func() error { return Stop(cmd) }
	}
	return nil
}

// Stop asks the supervisor to kill and reap its children before exiting.
func Stop(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGTERM)
}

func supervise(argv []string) int {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 125
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGWINCH)
	defer signal.Stop(signals)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// An interactive child must remain in the controlling terminal's foreground
	// group. A separate group would suspend provider input with SIGTTIN.
	foreground, terminalErr := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	interactive := terminalErr == nil && foreground == syscall.Getpgrp()
	if interactive {
		cmd.SysProcAttr = nil
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 125
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
wait:
	for {
		select {
		case <-done:
			break wait
		case sig := <-signals:
			if sig == syscall.SIGWINCH {
				if !interactive {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGWINCH)
				}
				continue
			}
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Process.Kill()
			<-done
			break wait
		}
	}
	// The leader is reaped. Any surviving children, including escaped groups,
	// are now ours. Killing each adopted child makes its children ours in turn.
	for {
		if err := killChildren(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 125
		}
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if err == syscall.ECHILD {
			break
		}
		if err != nil && err != syscall.EINTR {
			fmt.Fprintln(os.Stderr, err)
			return 125
		}
		if pid == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	code := cmd.ProcessState.ExitCode()
	if code < 0 {
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		return 125
	}
	return code
}

func killChildren() error {
	// Go can fork on any runtime thread; inspect every thread's child list.
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return err
	}
	for _, task := range tasks {
		data, err := os.ReadFile(filepath.Join("/proc/self/task", task.Name(), "children"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, field := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(field)
			if err != nil {
				return err
			}
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
				return err
			}
		}
	}
	return nil
}
