//go:build linux

// Package workerterminal attaches a driver-owned command to a terminal host.
package workerterminal

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type Host func(context.Context, string) error
type contextKey struct{}
type interactiveKey struct{}

// WithInteractive connects all native session descriptors directly to the PTY.
func WithInteractive(ctx context.Context) context.Context {
	return context.WithValue(ctx, interactiveKey{}, true)
}

func WithHost(ctx context.Context, host Host) context.Context {
	return context.WithValue(ctx, contextKey{}, host)
}

// Attach preserves the command envelope and output observers. The host receives
// only a private terminal socket, never a worker command or process authority.
func Attach(ctx context.Context, cmd *exec.Cmd) (func(), error) {
	host, _ := ctx.Value(contextKey{}).(Host)
	if host == nil {
		return func() {}, nil
	}
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	master := os.NewFile(uintptr(fd), "worker-pty")
	fail := func(err error) (func(), error) { master.Close(); return nil, err }
	if err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		return fail(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		return fail(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return fail(err)
	}
	dir, err := os.MkdirTemp("", "marshal-terminal-")
	if err != nil {
		slave.Close()
		return fail(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "terminal.sock"), Net: "unix"})
	if err != nil {
		slave.Close()
		os.RemoveAll(dir)
		return fail(err)
	}
	listener.SetDeadline(time.Now().Add(5 * time.Second))
	var mu sync.Mutex
	var connection net.Conn
	observer := cmd.Stdout
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		c, err := listener.Accept()
		if err != nil {
			return
		}
		mu.Lock()
		connection = c
		mu.Unlock()
		defer c.Close()
		go io.Copy(master, c)
		output := io.Writer(c)
		if interactive, _ := ctx.Value(interactiveKey{}).(bool); interactive && observer != nil {
			output = io.MultiWriter(c, observer)
		}
		io.Copy(output, master)
	}()
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			slave.Close()
			select {
			case <-drained:
			case <-time.After(200 * time.Millisecond):
			}
			listener.Close()
			master.Close()
			mu.Lock()
			if connection != nil {
				connection.Close()
			}
			mu.Unlock()
			os.RemoveAll(dir)
		})
	}
	if err := host(ctx, filepath.Join(dir, "terminal.sock")); err != nil {
		cleanup()
		return nil, err
	}
	if interactive, _ := ctx.Value(interactiveKey{}).(bool); interactive {
		// os/exec must pass the file itself, not a writer that creates a pipe.
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		_ = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 120})
		return cleanup, nil
	}
	// Keep explicit provider stdin (a request document) unchanged.
	if cmd.Stdin == nil {
		cmd.Stdin = slave
	}
	cmd.Stdout = io.MultiWriter(cmd.Stdout, slave)
	cmd.Stderr = io.MultiWriter(cmd.Stderr, slave)
	return cleanup, nil
}
