//go:build !governed && unix

// Package proposalmcp owns the live Marshal proposal MCP transport: a private
// same-user Unix socket with a random 256-bit bearer capability, strict JSON
// decoding, and the client/server halves the TUI broker and the stdio MCP child
// share. Authority (launch leases, project/provider/session identity, store
// receipts) lives outside this file; the transport only carries a verified
// capability plus a method name and raw argument bytes.
package proposalmcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// maxRequestBytes bounds a single transport frame before parsing. It is
	// deliberately small: proposals are short action payloads, and an oversized
	// frame is rejected rather than buffered.
	maxRequestBytes = 16384

	// transportTimeout bounds dial, read and write on every connection. It is a
	// safety net; callers that carry a real deadline pass it through Call.
	transportTimeout = 30 * time.Second

	// baseDir is the fixed parent for both the listening socket directory and
	// the separate config directory. Both are created 0700 with 0600 leaves.
	baseDir = "/var/tmp/mx"
)

// Config is the immutable private endpoint configuration a launch hands to its
// stdio MCP child. Endpoint is the Unix socket path; Capability is the random
// 256-bit bearer (64 lowercase hex characters). Neither is derived from the
// project or the caller.
type Config struct {
	Namespace   string `json:"state_namespace"`
	ProjectID   string `json:"project_id,omitempty"`
	ProjectRoot string `json:"project_root,omitempty"`
	Endpoint    string `json:"endpoint"`
	Capability  string `json:"capability"`
}

// Handler receives a verified request: only the method name and the raw,
// unmodified argument bytes. The capability is consumed by the transport and
// never reaches the Handler. The context is cancelled on broker shutdown or
// after the per-connection deadline.
type Handler func(context.Context, string, json.RawMessage) (json.RawMessage, error)

// requestEnvelope is the wire frame. DecodeStrict rejects duplicate object
// keys, unknown fields and trailing data, so a client cannot smuggle identity
// or trust labels outside these three fields.
type requestEnvelope struct {
	Namespace  string          `json:"state_namespace"`
	Capability string          `json:"capability"`
	Method     string          `json:"method"`
	Args       json.RawMessage `json:"args"`
	RawArgs    string          `json:"raw_args,omitempty"`
}

type responseEnvelope struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type server struct {
	ln         net.Listener
	dir        string
	socketPath string
	namespace  string
	capability string
	handler    Handler
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	connMu     sync.Mutex
	conns      map[net.Conn]struct{}
	closeOnce  sync.Once
	acceptDone chan struct{}
}

// Listen creates a short 0700 temporary directory under /var/tmp/mx, binds a
// 0600 Unix socket inside it, generates a random 256-bit bearer capability, and
// begins accepting one request per connection. The returned cleanup closes the
// listener and every active connection, then removes the directory. Cleanup is
// idempotent.
func Listen(handler Handler) (Config, func(), error) {
	if handler == nil {
		return Config{}, nil, errors.New("proposalmcp: nil handler")
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return Config{}, nil, fmt.Errorf("proposalmcp: create transport base dir: %w", err)
	}
	dir, err := os.MkdirTemp(baseDir, "proposal-")
	if err != nil {
		return Config{}, nil, fmt.Errorf("proposalmcp: create transport dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return Config{}, nil, fmt.Errorf("proposalmcp: restrict transport dir: %w", err)
	}
	socketPath := filepath.Join(dir, "broker.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return Config{}, nil, fmt.Errorf("proposalmcp: listen: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = ln.Close()
		_ = os.RemoveAll(dir)
		return Config{}, nil, fmt.Errorf("proposalmcp: restrict socket: %w", err)
	}
	capability, err := randomCapability()
	if err != nil {
		_ = ln.Close()
		_ = os.RemoveAll(dir)
		return Config{}, nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &server{
		ln:         ln,
		dir:        dir,
		socketPath: socketPath,
		namespace:  statepath.DirName,
		capability: capability,
		handler:    handler,
		ctx:        ctx,
		cancel:     cancel,
		conns:      map[net.Conn]struct{}{},
		acceptDone: make(chan struct{}),
	}
	go s.acceptLoop()
	cleanup := func() { s.close() }
	return Config{Namespace: statepath.DirName, Endpoint: socketPath, Capability: capability}, cleanup, nil
}

func (s *server) acceptLoop() {
	defer close(s.acceptDone)
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.connMu.Lock()
		s.conns[conn] = struct{}{}
		s.connMu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				s.connMu.Lock()
				delete(s.conns, conn)
				s.connMu.Unlock()
				_ = conn.Close()
			}()
			s.handleConn(conn)
		}()
	}
}

func (s *server) close() {
	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.ln.Close()
		<-s.acceptDone
		s.connMu.Lock()
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.connMu.Unlock()
		s.wg.Wait()
		_ = os.RemoveAll(s.dir)
	})
}

func (s *server) handleConn(conn net.Conn) {
	ctx, cancel := context.WithTimeout(s.ctx, transportTimeout)
	defer cancel()
	_ = conn.SetDeadline(time.Now().Add(transportTimeout))

	data, err := readLine(conn)
	if err != nil {
		s.writeResponse(conn, responseEnvelope{Error: "invalid request: " + err.Error()})
		return
	}
	var env requestEnvelope
	if err := DecodeStrict(data, &env); err != nil {
		s.writeResponse(conn, responseEnvelope{Error: "invalid request: " + err.Error()})
		return
	}
	if env.Namespace != s.namespace {
		s.writeResponse(conn, responseEnvelope{Error: "state namespace binding differs"})
		return
	}
	if env.Method == "" {
		s.writeResponse(conn, responseEnvelope{Error: "invalid request: empty method"})
		return
	}
	if !constantTimeEqual(env.Capability, s.capability) {
		s.writeResponse(conn, responseEnvelope{Error: "unauthorized"})
		return
	}
	raw := env.Args
	if env.RawArgs != "" {
		if len(env.Args) != 0 && string(env.Args) != "null" {
			s.writeResponse(conn, responseEnvelope{Error: "ambiguous arguments"})
			return
		}
		raw = json.RawMessage(env.RawArgs)
		var checked json.RawMessage
		if err := DecodeStrict(raw, &checked); err != nil {
			s.writeResponse(conn, responseEnvelope{Error: "invalid arguments"})
			return
		}
	}
	result, err := s.handler(ctx, env.Method, raw)
	if err != nil {
		s.writeResponse(conn, responseEnvelope{Error: err.Error()})
		return
	}
	s.writeResponse(conn, responseEnvelope{Result: result})
}

func (s *server) writeResponse(conn net.Conn, resp responseEnvelope) {
	data, err := json.Marshal(resp)
	if err != nil {
		data = []byte(`{"error":"internal transport error"}`)
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

// Call sends one request over a fresh connection to the endpoint and returns
// the raw result bytes. A transport error can occur after durable admission;
// retry with the same submission ID or inspect its receipt before resubmitting.
func Call(ctx context.Context, cfg Config, method string, args json.RawMessage) (json.RawMessage, error) {
	if cfg.Namespace != statepath.DirName {
		return nil, errors.New("proposalmcp: state namespace binding differs")
	}
	if cfg.Endpoint == "" || cfg.Capability == "" {
		return nil, errors.New("proposalmcp: incomplete client config")
	}
	if method == "" {
		return nil, errors.New("proposalmcp: empty method")
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("proposalmcp: dial: %w", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(transportTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	// Closing the connection on cancellation unblocks a read that a deadline
	// alone would leave pending until transportTimeout.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	env := requestEnvelope{Namespace: cfg.Namespace, Capability: cfg.Capability, Method: method, RawArgs: string(args)}
	data, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("proposalmcp: encode request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("proposalmcp: write request: %w", err)
	}

	resp, err := readLine(conn)
	if err != nil {
		return nil, fmt.Errorf("proposalmcp: read response: %w", err)
	}
	var re responseEnvelope
	if err := DecodeStrict(resp, &re); err != nil {
		return nil, fmt.Errorf("proposalmcp: decode response: %w", err)
	}
	if re.Error != "" {
		return nil, errors.New(re.Error)
	}
	return re.Result, nil
}

// SaveConfig writes the private endpoint configuration to a new 0700 short
// temporary directory outside the project, in a 0600 file. The returned
// cleanup removes the directory. The capability is never printed; the 0600
// file is the only place it is written, for the stdio child alone to read.
func SaveConfig(cfg Config) (string, func(), error) {
	if cfg.Namespace != statepath.DirName {
		return "", nil, errors.New("proposalmcp: state namespace binding differs")
	}
	if cfg.Endpoint == "" || cfg.Capability == "" {
		return "", nil, errors.New("proposalmcp: incomplete client config")
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("proposalmcp: create config base dir: %w", err)
	}
	dir, err := os.MkdirTemp(baseDir, "proposal-cfg-")
	if err != nil {
		return "", nil, fmt.Errorf("proposalmcp: create config dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("proposalmcp: restrict config dir: %w", err)
	}
	path := filepath.Join(dir, "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("proposalmcp: encode config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("proposalmcp: write config: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	return path, cleanup, nil
}

// LoadConfig reads a private endpoint configuration written by SaveConfig and
// returns it only if the JSON is strict (no duplicate keys, no unknown fields,
// no trailing data).
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("proposalmcp: read config: %w", err)
	}
	var cfg Config
	if err := DecodeStrict(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("proposalmcp: decode config: %w", err)
	}
	if cfg.Namespace != statepath.DirName {
		return Config{}, errors.New("proposalmcp: state namespace binding differs")
	}
	return cfg, nil
}

func randomCapability() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("proposalmcp: generate capability: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// readLine reads one newline-terminated frame, bounded at maxRequestBytes. A
// frame larger than the bound, or a peer that closes without a terminator, is
// rejected.
func readLine(conn net.Conn) ([]byte, error) {
	buf := make([]byte, maxRequestBytes+1)
	n := 0
	for {
		if n >= len(buf) {
			return nil, fmt.Errorf("request exceeds %d bytes", maxRequestBytes)
		}
		m, err := conn.Read(buf[n:])
		if m > 0 {
			if idx := bytes.IndexByte(buf[n:n+m], '\n'); idx >= 0 {
				n += idx
				break
			}
			n += m
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) && n > 0 {
				break
			}
			return nil, err
		}
	}
	data := bytes.TrimRight(buf[:n], "\r\n")
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("empty request")
	}
	return data, nil
}
