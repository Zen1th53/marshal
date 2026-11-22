//go:build linux || darwin

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
	"syscall"
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

// acceptTimeout bounds how long the hosted relay may take to connect.
var acceptTimeout = 60 * time.Second

// Attach preserves the command envelope and output observers. The host receives
// only a private terminal socket, never a worker command or process authority.
func Attach(ctx context.Context, cmd *exec.Cmd) (func(), error) {
	host, _ := ctx.Value(contextKey{}).(Host)
	if host == nil {
		return func() {}, nil
	}
	master, fd, slave, err := openPTY()
	if err != nil {
		return nil, err
	}
	fail := func(err error) (func(), error) { master.Close(); return nil, err }
	dir, err := os.MkdirTemp(socketParent(), "marshal-terminal-")
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
	// The host opens the pane (tmux) before the relay in it connects; on a
	// slow host that alone can take many seconds, and a relay that missed
	// the accept left the worker's terminal undrained. Bounded, not short.
	listener.SetDeadline(time.Now().Add(acceptTimeout))
	var mu sync.Mutex
	var connection net.Conn
	observer := cmd.Stdout
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		c, err := listener.Accept()
		if err != nil {
			// No relay will read the terminal. Keep draining it so the
			// worker never blocks on a full terminal buffer; on macOS a
			// session leader cannot even exit while its output is unread.
			io.Copy(io.Discard, master)
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
	stopResize := func() {}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			stopResize()
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
	if size, ok := ctx.Value(sizeKey{}).(Size); ok {
		stopResize, err = propagateTerminalSize(ctx, fd, size)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("hosted terminal size: %w", err)
		}
	}

	if interactive, _ := ctx.Value(interactiveKey{}).(bool); interactive {
		configureInteractivePTY(cmd, slave)
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

// maxSocketPath keeps the relay socket within sun_path on every supported
// kernel (104 bytes on macOS, 108 on Linux).
const maxSocketPath = 100

// socketParent is the temporary directory unless its path would leave the
// relay socket too long to bind (a deep per-user TMPDIR on macOS).
func socketParent() string {
	parent := os.TempDir()
	if len(filepath.Join(parent, "marshal-terminal-0123456789", "terminal.sock")) > maxSocketPath {
		return "/tmp"
	}
	return parent
}

// TIOCSWINSZ updates provider layout and signals the foreground process group.
func propagateTerminalSize(ctx context.Context, fd int, size Size) (func(), error) {
	var previous unix.Winsize
	resize := func() error {
		child, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		rows, cols, err := size(child)
		next := unix.Winsize{Row: rows, Col: cols}
		if err != nil || rows == 0 || cols == 0 {
			if previous.Row != 0 && previous.Col != 0 {
				return nil // Keep the last real size while the host is unreadable.
			}
			next = unix.Winsize{Row: 40, Col: 120}
		}
		if next != previous {
			if err := unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &next); err != nil {
				return err
			}
			previous = next
		}
		return nil
	}
	if err := resize(); err != nil {
		return func() {}, err
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				resize()
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done); wg.Wait() }) }, nil
}

func configureInteractivePTY(cmd *exec.Cmd, slave *os.File) {
	// TIOCSWINSZ delivers SIGWINCH to the supervisor and its foreground provider.
	attr := cmd.SysProcAttr
	if attr == nil {
		attr = &syscall.SysProcAttr{}
	}
	// Preserve the supervisor's parent-death signal while making it the PTY
	// session leader. Setsid supplies its private process group.
	attr.Setpgid = false
	attr.Setsid = true
	attr.Setctty = true
	attr.Ctty = 0
	cmd.SysProcAttr = attr
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
}
