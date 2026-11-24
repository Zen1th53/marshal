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
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
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
	// frame is rejected rather than buffered. It counts the encoded frame
	// body only: the socket's terminating newline is not part of it, and a
	// spooled frame has none. Requests and responses share the bound.
	maxRequestBytes = MaxFrameBytes

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
	// Spool is the private request directory of a broker that also serves
	// requests as files (ListenWith, Options.SpoolParent), "" otherwise. A
	// client whose sandbox refuses connect(2) on Unix sockets uses it.
	Spool string `json:"spool,omitempty"`
}

// Options tunes ListenWith.
type Options struct {
	// SpoolParent, when set, makes the broker also serve requests written as
	// files to a new private directory under it. Codex runs Marshal's stdio
	// MCP child inside the chat's sandbox, and that sandbox refuses
	// connect(2) on every Unix socket unless network access is opened
	// (seccomp on Linux, Seatbelt on macOS); the directory must be one the
	// sandbox already lets the child write. Nothing else is opened.
	SpoolParent string
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
	// Limit is the byte cap behind a refusal, so the caller receives the
	// same typed limit the handler raised rather than its text alone.
	Limit *model.Limit `json:"limit,omitempty"`
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
	// spool is the request directory (spool.go), "" when not served.
	spool     string
	spoolDone chan struct{}
}

// Listen creates a short 0700 temporary directory under /var/tmp/mx, binds a
// 0600 Unix socket inside it, generates a random 256-bit bearer capability, and
// begins accepting one request per connection. The returned cleanup closes the
// listener and every active connection, then removes the directory. Cleanup is
// idempotent.
func Listen(handler Handler) (Config, func(), error) {
	return ListenWith(handler, Options{})
}

// ListenWith is Listen with opts; see Options.
func ListenWith(handler Handler, opts Options) (Config, func(), error) {
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
	cfg := Config{Namespace: statepath.DirName, Endpoint: socketPath, Capability: capability}
	if opts.SpoolParent != "" {
		spool, err := newSpoolDir(opts.SpoolParent)
		if err != nil {
			cancel()
			_ = ln.Close()
			_ = os.RemoveAll(dir)
			return Config{}, nil, err
		}
		s.spool, s.spoolDone = spool, make(chan struct{})
		cfg.Spool = spool
		go s.spoolLoop()
	}
	go s.acceptLoop()
	cleanup := func() { s.close() }
	return cfg, cleanup, nil
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
		if s.spoolDone != nil {
			<-s.spoolDone
		}
		s.connMu.Lock()
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.connMu.Unlock()
		s.wg.Wait()
		_ = os.RemoveAll(s.dir)
		if s.spool != "" {
			_ = os.RemoveAll(s.spool)
		}
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
	s.writeResponse(conn, s.serve(ctx, data))
}

// serve verifies one request frame and runs it: the same checks for a
// socket connection and a spooled file.
func (s *server) serve(ctx context.Context, data []byte) responseEnvelope {
	var env requestEnvelope
	if err := DecodeStrict(data, &env); err != nil {
		return responseEnvelope{Error: "invalid request: " + err.Error()}
	}
	if env.Namespace != s.namespace {
		return responseEnvelope{Error: "state namespace binding differs"}
	}
	if env.Method == "" {
		return responseEnvelope{Error: "invalid request: empty method"}
	}
	if env.Method == NoticeMethod && len(data) > MaxNoticeFrameBytes {
		return responseEnvelope{Error: "invalid request: limit notice too large"}
	}
	if !constantTimeEqual(env.Capability, s.capability) {
		return responseEnvelope{Error: "unauthorized"}
	}
	raw := env.Args
	if env.RawArgs != "" {
		if len(env.Args) != 0 && string(env.Args) != "null" {
			return responseEnvelope{Error: "ambiguous arguments"}
		}
		raw = json.RawMessage(env.RawArgs)
		var checked json.RawMessage
		if err := DecodeStrict(raw, &checked); err != nil {
			return responseEnvelope{Error: "invalid arguments"}
		}
	}
	result, err := s.handler(ctx, env.Method, raw)
	if err != nil {
		resp := responseEnvelope{Error: err.Error()}
		if limit, ok := model.LimitOf(err); ok {
			resp.Limit = &limit
		}
		return resp
	}
	return responseEnvelope{Result: result}
}

// encodeResponse is resp as one frame body within the shared frame bound.
// A result whose frame would not fit is replaced by a compact typed refusal
// that asks for a smaller page, so the reader never sees a frame it rejects.
func encodeResponse(resp responseEnvelope) []byte {
	data, err := json.Marshal(resp)
	if err != nil {
		return []byte(`{"error":"internal transport error"}`)
	}
	if len(data) <= maxRequestBytes {
		return data
	}
	limit := model.Limit{Kind: LimitKindResponse, Subject: "response frame", ActualBytes: int64(len(data)), CapBytes: maxRequestBytes, Limited: true,
		Action: "Ask for a smaller page (a lower limit) so the reply fits."}
	data, err = json.Marshal(responseEnvelope{Error: projectcontext.ErrBudget.Error() + ": the reply frame is too large", Limit: &limit})
	if err != nil {
		return []byte(`{"error":"internal transport error"}`)
	}
	return data
}

func (s *server) writeResponse(conn net.Conn, resp responseEnvelope) {
	data := append(encodeResponse(resp), '\n')
	_, _ = conn.Write(data)
}

// Call sends one request over a fresh connection to the endpoint and returns
// the raw result bytes. A transport error can occur after durable admission;
// retry with the same submission ID or inspect its receipt before resubmitting.
//
// A request whose encoded frame exceeds the frame bound is never sent. Call
// returns a typed limit for it and tells the broker in a separate compact
// notice (see sendLimitNotice) that carries no part of the refused request.
func Call(ctx context.Context, cfg Config, method string, args json.RawMessage) (json.RawMessage, error) {
	data, err := encodeRequest(cfg, method, args)
	if err != nil {
		return nil, err
	}
	if len(data) > maxRequestBytes {
		return nil, refuseFrame(ctx, cfg, method, args, len(data))
	}
	resp, err := send(ctx, cfg, data)
	if err != nil {
		return nil, err
	}
	return decodeResponse(resp)
}

// encodeRequest is the exact frame body Call writes for method and args: the
// one byte count every preflight measures.
func encodeRequest(cfg Config, method string, args json.RawMessage) ([]byte, error) {
	if cfg.Namespace != statepath.DirName {
		return nil, errors.New("proposalmcp: state namespace binding differs")
	}
	if cfg.Endpoint == "" || cfg.Capability == "" {
		return nil, errors.New("proposalmcp: incomplete client config")
	}
	if method == "" {
		return nil, errors.New("proposalmcp: empty method")
	}
	env := requestEnvelope{Namespace: cfg.Namespace, Capability: cfg.Capability, Method: method, RawArgs: string(args)}
	data, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("proposalmcp: encode request: %w", err)
	}
	return data, nil
}

// send writes one encoded frame to the broker, through the socket or, when
// the caller's sandbox refuses the socket, the spool, and returns the raw
// response frame.
func send(ctx context.Context, cfg Config, data []byte) ([]byte, error) {
	if len(data) > maxRequestBytes {
		return nil, fmt.Errorf("proposalmcp: request exceeds %d bytes", maxRequestBytes)
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", cfg.Endpoint)
	if err != nil {
		if cfg.Spool != "" && sandboxRefusedDial(err) {
			// The caller's sandbox refuses Unix sockets; the broker
			// reads the same frame from its spool.
			return callSpool(ctx, cfg.Spool, data)
		}
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

	frame := append(append([]byte(nil), data...), '\n')
	if _, err := conn.Write(frame); err != nil {
		return nil, fmt.Errorf("proposalmcp: write request: %w", err)
	}

	resp, err := readLine(conn)
	if err != nil {
		return nil, fmt.Errorf("proposalmcp: read response: %w", err)
	}
	return resp, nil
}

// decodeResponse is a response frame's result, or its error. A refusal that
// carries a limit is returned as that typed limit.
func decodeResponse(resp []byte) (json.RawMessage, error) {
	var re responseEnvelope
	if err := DecodeStrict(resp, &re); err != nil {
		return nil, fmt.Errorf("proposalmcp: decode response: %w", err)
	}
	if re.Error != "" {
		if re.Limit != nil {
			return nil, model.NewLimitError(errors.New(re.Error), *re.Limit)
		}
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
