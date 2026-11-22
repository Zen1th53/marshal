//go:build linux || darwin

package workerterminal

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// relayHost is a fake terminal host: it dials the private socket the way the
// tmux pane's socat relay does and keeps everything the worker terminal sends.
type relayHost struct {
	mu     sync.Mutex
	out    bytes.Buffer
	conn   net.Conn
	socket string
	done   chan struct{}
}

func newRelayHost() *relayHost { return &relayHost{done: make(chan struct{})} }

func (h *relayHost) host(_ context.Context, socket string) error {
	h.socket = socket
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return err
	}
	h.conn = conn
	go func() {
		defer close(h.done)
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			h.mu.Lock()
			h.out.Write(buf[:n])
			h.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return nil
}

func (h *relayHost) output() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.out.String()
}

func (h *relayHost) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(h.output(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("relay never received %q; got %q", want, h.output())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *relayHost) waitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay connection stayed open after the terminal closed")
	}
}

// A hosted worker runs through the PTY transport: its output reaches the
// host's relay and the driver's own observer, its exit is reported, and
// closing the terminal ends the relay and removes the private socket.
func TestAttachHostedWorkerOutputExitAndClose(t *testing.T) {
	relay := newRelayHost()
	ctx := WithHost(t.Context(), relay.host)
	cmd := exec.Command("/bin/sh", "-c", `printf 'hello-from-worker\n'; printf 'to-stderr\n' >&2; exit 3`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	closeTerminal, err := Attach(ctx, cmd)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := cmd.Start(); err != nil {
		closeTerminal()
		t.Fatal(err)
	}
	err = cmd.Wait()
	var exitErr *exec.ExitError
	if !asExit(err, &exitErr) || exitErr.ExitCode() != 3 {
		closeTerminal()
		t.Fatalf("worker exit = %v; want exit status 3", err)
	}
	relay.waitFor(t, "hello-from-worker")
	relay.waitFor(t, "to-stderr")
	if !strings.Contains(stdout.String(), "hello-from-worker") || !strings.Contains(stderr.String(), "to-stderr") {
		t.Fatalf("driver observers lost output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	closeTerminal()
	closeTerminal() // idempotent
	relay.waitClosed(t)
	if _, err := os.Stat(filepath.Dir(relay.socket)); !os.IsNotExist(err) {
		t.Fatalf("terminal socket directory survived close: %v", err)
	}
}

// An interactive session owns the PTY as its controlling terminal: input
// typed into the host relay reaches the worker and its reply comes back,
// including output written just before exit.
func TestAttachInteractiveWorkerInputAndExit(t *testing.T) {
	relay := newRelayHost()
	ctx := WithInteractive(WithHost(t.Context(), relay.host))
	cmd := exec.Command("/bin/sh", "-c", `test -t 0 || exit 9; printf 'ready\n'; IFS= read -r line; printf 'got:%s\n' "$line"`)
	var observed bytes.Buffer
	var mu sync.Mutex
	cmd.Stdout = writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return observed.Write(p) })
	closeTerminal, err := Attach(ctx, cmd)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer closeTerminal()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	relay.waitFor(t, "ready")
	if _, err := io.WriteString(relay.conn, "operator\n"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("interactive worker: %v (relay %q)", err, relay.output())
	}
	relay.waitFor(t, "got:operator")
	closeTerminal()
	relay.waitClosed(t)
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(observed.String(), "got:operator") {
		t.Fatalf("interactive observer lost output: %q", observed.String())
	}
}

// A host that fails leaves nothing behind and the worker is never attached.
func TestAttachHostFailureCleansUp(t *testing.T) {
	var socket string
	ctx := WithHost(t.Context(), func(_ context.Context, s string) error { socket = s; return io.ErrClosedPipe })
	if _, err := Attach(ctx, exec.Command("/bin/true")); err == nil {
		t.Fatal("attach succeeded with a failing host")
	}
	if socket == "" {
		t.Fatal("host was never offered a socket")
	}
	if _, err := os.Stat(filepath.Dir(socket)); !os.IsNotExist(err) {
		t.Fatalf("socket directory survived a failed host: %v", err)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func asExit(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

// A relay that never connects must not wedge the worker: once the accept
// window closes the terminal is drained, so the worker can still exit.
func TestAttachWorkerExitsWhenRelayNeverConnects(t *testing.T) {
	previous := acceptTimeout
	acceptTimeout = 100 * time.Millisecond
	t.Cleanup(func() { acceptTimeout = previous })
	ctx := WithInteractive(WithHost(t.Context(), func(context.Context, string) error { return nil }))
	cmd := exec.Command("/bin/sh", "-c", `i=0; while [ $i -lt 200 ]; do printf 'unread output line %s\n' $i; i=$((i+1)); done`)
	closeTerminal, err := Attach(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTerminal()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("worker could not exit with nobody reading its terminal")
	}
}
