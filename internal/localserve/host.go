// Package localserve is the application-layer lifecycle boundary for the local
// servers an open workspace can host on its own runtime: the per-project
// runtime socket (`marshal daemon`), the MCP HTTP server (`marshal mcp serve`)
// and the A2A HTTP server (`marshal a2a serve`).
//
// It composes exactly what the CLI composes, with two deliberate narrowings:
// HTTP servers are always authenticated (there is no insecure mode here), and
// they bind loopback addresses only. A server lives as long as the Host that
// started it; closing the Host shuts every server down.
package localserve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Zen1th53/marshal/internal/a2a"
	"github.com/Zen1th53/marshal/internal/api"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/httpsrv"
	"github.com/Zen1th53/marshal/internal/mcp"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

// Kind names one hostable server.
type Kind string

const (
	Daemon Kind = "daemon"
	MCP    Kind = "mcp"
	A2A    Kind = "a2a"
)

// DefaultAddress is the CLI's default listen address for each HTTP server.
func DefaultAddress(kind Kind) string {
	switch kind {
	case MCP:
		return "127.0.0.1:8080"
	case A2A:
		return "127.0.0.1:8081"
	}
	return ""
}

// Status is what a running server is bound to.
type Status struct {
	Kind      Kind
	Address   string
	StartedAt time.Time
}

type running struct {
	status Status
	stop   func()
	done   chan struct{}
}

// Host owns the servers started over one runtime.
type Host struct {
	runtime *app.Runtime
	mu      sync.Mutex
	servers map[Kind]*running
	closed  bool
}

// New returns a Host for runtime. It starts nothing.
func New(runtime *app.Runtime) *Host {
	return &Host{runtime: runtime, servers: map[Kind]*running{}}
}

// Status reports a running server, if any.
func (h *Host) Status(kind Kind) (Status, bool) {
	if h == nil {
		return Status{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.servers[kind]
	if !ok {
		return Status{}, false
	}
	select {
	case <-r.done:
		delete(h.servers, kind)
		return Status{}, false
	default:
	}
	return r.status, true
}

// LoopbackAddress reports whether addr is a host:port on a loopback interface.
func LoopbackAddress(addr string) bool {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || port == "" {
		return false
	}
	host = strings.ToLower(host)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Start starts one server. It fails when the server is already running, the
// address is not loopback, or the listener cannot be bound; it never reports a
// server that is not accepting connections.
func (h *Host) Start(ctx context.Context, kind Kind, addr string) (Status, error) {
	if h == nil || h.runtime == nil {
		return Status{}, fmt.Errorf("%w: no runtime is open", model.ErrUnavailable)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return Status{}, fmt.Errorf("%w: the workspace is closing", model.ErrUnavailable)
	}
	if r, ok := h.servers[kind]; ok {
		select {
		case <-r.done:
			delete(h.servers, kind)
		default:
			return Status{}, fmt.Errorf("%w: the %s server is already running on %s", model.ErrConflict, kind, r.status.Address)
		}
	}
	var (
		r   *running
		err error
	)
	switch kind {
	case MCP, A2A:
		r, err = h.startHTTP(kind, addr)
	case Daemon:
		r, err = h.startDaemon()
	default:
		err = fmt.Errorf("%w: unknown server %q", model.ErrInvalid, kind)
	}
	if err != nil {
		return Status{}, err
	}
	h.servers[kind] = r
	return r.status, nil
}

func (h *Host) startHTTP(kind Kind, addr string) (*running, error) {
	if !LoopbackAddress(addr) {
		return nil, fmt.Errorf("%w: %q is not a loopback host:port", model.ErrInvalid, addr)
	}
	layout, err := project.Discover(h.runtime.ProjectRoot())
	if err != nil {
		return nil, err
	}
	authMgr := auth.NewManager(layout.RuntimeDir)
	var server *httpsrv.Server
	switch kind {
	case MCP:
		server = httpsrv.NewServer(httpsrv.Config{Handler: mcp.NewServerWithAuth(h.runtime, authMgr).Handler()})
	default:
		srv, err := a2a.NewServerWithAuth(h.runtime, authMgr)
		if err != nil {
			return nil, err
		}
		server = httpsrv.NewServer(httpsrv.Config{Handler: srv.Handler()})
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	return &running{
		status: Status{Kind: kind, Address: listener.Addr().String(), StartedAt: time.Now().UTC()},
		done:   done,
		stop: func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
			_ = listener.Close()
		},
	}, nil
}

// startDaemon serves the runtime socket the CLI talks to, with the same PID
// file discipline as `marshal daemon`: a live owner is a conflict, a stale one
// is cleared.
func (h *Host) startDaemon() (*running, error) {
	layout, err := project.Discover(h.runtime.ProjectRoot())
	if err != nil {
		return nil, err
	}
	if data, readErr := os.ReadFile(layout.PID); readErr == nil {
		var oldPID int
		if _, scanErr := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &oldPID); scanErr == nil && oldPID > 0 {
			if killErr := syscall.Kill(oldPID, 0); errors.Is(killErr, syscall.ESRCH) {
				_ = os.Remove(layout.PID)
				_ = os.Remove(layout.Socket)
			}
		}
	}
	pid, err := os.OpenFile(layout.PID, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: a daemon already owns this project (%v)", model.ErrConflict, err)
	}
	if _, err := fmt.Fprintln(pid, os.Getpid()); err != nil {
		pid.Close()
		os.Remove(layout.PID)
		return nil, err
	}
	if err := pid.Close(); err != nil {
		os.Remove(layout.PID)
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	serveErr := make(chan error, 1)
	go func() {
		defer close(done)
		defer os.Remove(layout.PID)
		serveErr <- api.NewServer(h.runtime).Serve(ctx, layout.Socket)
	}()
	// Report running only once the socket accepts a connection.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if conn, dialErr := net.DialTimeout("unix", layout.Socket, 200*time.Millisecond); dialErr == nil {
			conn.Close()
			break
		}
		select {
		case err := <-serveErr:
			cancel()
			if err == nil {
				err = errors.New("the runtime socket closed before it accepted a connection")
			}
			return nil, err
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			return nil, fmt.Errorf("%w: the runtime socket did not start accepting connections", model.ErrUnavailable)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return &running{
		status: Status{Kind: Daemon, Address: layout.Socket, StartedAt: time.Now().UTC()},
		done:   done,
		stop: func() {
			cancel()
			<-done
		},
	}, nil
}

// Close stops every server this Host started and refuses new ones.
func (h *Host) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.closed = true
	servers := h.servers
	h.servers = map[Kind]*running{}
	h.mu.Unlock()
	for _, r := range servers {
		r.stop()
	}
}
