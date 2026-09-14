package codex

// This file intentionally exposes a very small, typed subset of the Codex
// app-server protocol.  The upstream protocol also contains generic shell,
// filesystem, config, plugin, and remote-control methods.  None of those are
// a MARSHAL execution surface: Process 05 remains the sole authority for tool
// execution, approvals, sandboxing, evidence, and cancellation.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

const appServerProtocolVersion = "v2"

// AppServerSession is the narrow durable-session reference MARSHAL may retain.
// It never includes Codex's host path, raw configuration, credentials, or
// arbitrary protocol payload.
type AppServerSession struct {
	ThreadID   string
	SessionID  string
	Model      string
	Worktree   string
	Protocol   string
	Persistent bool
}

// AppServerTurn is a typed reference to a Codex turn. The future execution
// bridge may store the IDs as evidence, but it must never persist raw protocol
// requests or credential-bearing server messages.
type AppServerTurn struct {
	ThreadID string
	TurnID   string
}

// AppServerTurnResult is a redacted, typed projection of terminal turn state.
// Raw app-server messages remain in the provider process and are never copied
// into MARSHAL state without its normal evidence sanitization boundary.
type AppServerTurnResult struct {
	ThreadID     string
	TurnID       string
	Status       string
	FinalText    string
	ErrorMessage string
}

// AppServerApprovalRequest is an exact, digest-bound request emitted by Codex.
// It is only an observation: a separate canonical MARSHAL authority must make
// any approval decision. The adapter has no accepting response method.
type AppServerApprovalRequest struct {
	RequestID  string
	Method     string
	ThreadID   string
	TurnID     string
	ItemID     string
	ApprovalID string
	Kind       string
	Command    string
	Worktree   string
	Digest     string
	// wireID preserves the JSON-RPC request identifier exactly as it arrived.
	// JSON-RPC permits either string or numeric IDs; responding with a string
	// for an incoming number leaves Codex waiting on an unmatched request.
	// It remains connection-local and is deliberately not exported/persisted.
	wireID json.RawMessage
}

type AppServerApprovalRequiredError struct{ Request AppServerApprovalRequest }

func (e *AppServerApprovalRequiredError) Error() string {
	return fmt.Sprintf("%s: Codex requested approval for %s", model.ErrPolicyDenied, e.Request.Method)
}

func (e *AppServerApprovalRequiredError) Unwrap() error { return model.ErrPolicyDenied }

// AppServerApprovalAuthority is implemented only by the canonical MARSHAL
// execution bridge.  The app-server client never receives a boolean or an
// arbitrary "accept" option from a UI caller: it asks this authority to
// validate and consume the exact native request before it responds to Codex.
//
// The request includes the native thread, turn, item, worktree and digest, so
// an approval for one tool invocation cannot authorize a changed invocation.
type AppServerApprovalAuthority interface {
	ApproveAppServerRequest(context.Context, AppServerApprovalRequest) error
}

// appServerThreadStartParams deliberately contains only typed execution
// settings.  In particular, this adapter has no exported generic RPC or shell
// entry point.
func appServerThreadStartParams(request adapter.Request) (map[string]any, error) {
	if request.TaskID == "" || request.Title == "" || request.Worktree == "" {
		return nil, fmt.Errorf("%w: incomplete Codex app-server session request", model.ErrInvalid)
	}
	if !filepath.IsAbs(request.Worktree) || filepath.Clean(request.Worktree) != request.Worktree {
		return nil, fmt.Errorf("%w: Codex app-server worktree must be a clean absolute path", model.ErrInvalid)
	}
	if err := ValidateDangerousFlags([]string{request.TaskID, request.Title, request.Worktree, request.Model}); err != nil {
		return nil, err
	}
	prompt, err := buildPrompt(request)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"model":                 nullableString(request.Model),
		"cwd":                   request.Worktree,
		"sandbox":               "workspace-write",
		"approvalPolicy":        "untrusted",
		"approvalsReviewer":     "user",
		"developerInstructions": string(prompt),
		// Durable threads are required for canonical crash/restart continuation.
		// Their use remains bounded by the re-injected MARSHAL envelope above.
		"ephemeral": false,
	}, nil
}

func appServerThreadResumeParams(threadID string, request adapter.Request) (map[string]any, error) {
	if threadID == "" {
		return nil, fmt.Errorf("%w: Codex app-server thread ID is required", model.ErrInvalid)
	}
	params, err := appServerThreadStartParams(request)
	if err != nil {
		return nil, err
	}
	params["threadId"] = threadID
	params["excludeTurns"] = true
	return params, nil
}

func appServerTurnStartParams(threadID string, request adapter.Request) (map[string]any, error) {
	if threadID == "" {
		return nil, fmt.Errorf("%w: Codex app-server thread ID is required", model.ErrInvalid)
	}
	// Reuse the strict request validation, then send a fixed typed instruction.
	// The task envelope is already re-injected as thread developer instructions;
	// a caller cannot turn this into an arbitrary shell or policy payload.
	if _, err := appServerThreadStartParams(request); err != nil {
		return nil, err
	}
	return map[string]any{
		"threadId":          threadID,
		"cwd":               request.Worktree,
		"model":             nullableString(request.Model),
		"approvalPolicy":    "untrusted",
		"approvalsReviewer": "user",
		// The app-server schema requires an explicit turn-level policy for
		// a client that owns governed execution. Thread-level sandbox mode
		// alone does not bind this particular turn's writable root or network
		// access. Re-inject both on every turn, including a resumed thread.
		"sandboxPolicy": map[string]any{
			"type":          "workspaceWrite",
			"writableRoots": []string{request.Worktree},
			"networkAccess": false,
		},
		"input": []map[string]any{{
			"type": "text",
			// The full immutable envelope remains in developerInstructions. The
			// native turn also needs a concrete user-task reference; a generic
			// "execute the envelope" instruction lets a model finish without
			// addressing the actual planned outcome. This is typed task text,
			// not a shell/program RPC surface.
			"text":          "Execute this exact MARSHAL-governed task: " + request.Title + "\nWork only within the developer-envelope constraints. Use approved built-in tools when needed, and do not claim completion unless the requested worktree outcome exists.",
			"text_elements": []any{},
		}},
	}, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// AppServer is a local stdio client. It starts no listener and explicitly
// never uses the upstream daemon, proxy, websocket, or remote-control routes.
// Unlike `codex exec`, Codex 0.154.0 exposes no --ignore-user-config switch
// for app-server, so this experimental path is not a replacement for the
// fully isolated Process 05 exec adapter until that host-config boundary is
// available and verified.
type AppServer struct {
	binary        string
	clientVersion string
	codexHome     string
	mu            sync.Mutex
	connection    *appServerConnection
}

func NewAppServer(binary, clientVersion string) *AppServer {
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		if governed, err := EnsureGovernedCodexHome(); err == nil && governed != "" {
			home = governed
		}
	}
	return &AppServer{binary: binary, clientVersion: clientVersion, codexHome: home}
}

func (s *AppServer) StartSession(ctx context.Context, request adapter.Request) (AppServerSession, error) {
	if err := RequireAppServerConfigFree(); err != nil {
		return AppServerSession{}, err
	}
	params, err := appServerThreadStartParams(request)
	if err != nil {
		return AppServerSession{}, err
	}
	response, err := s.call(ctx, "thread/start", params)
	if err != nil {
		return AppServerSession{}, err
	}
	return appServerSessionFromResponse(response, request.Worktree)
}

// GovernedCodexHomeDir returns the path to MARSHAL's managed config-free CODEX_HOME.
func GovernedCodexHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".marshal", "codex_appserver")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("marshal-codex-appserver-%d", os.Getuid()))
}

// EnsureGovernedCodexHome initializes and returns an isolated, config-free CODEX_HOME
// for MARSHAL execution. It automatically mirrors host credentials (auth.json), version info,
// and cached models from ~/.codex so the operator does not need to re-login or modify
// their personal ~/.codex/config.toml. It guarantees no config.toml exists in the
// returned directory.
func EnsureGovernedCodexHome() (string, error) {
	target := GovernedCodexHomeDir()
	if err := os.MkdirAll(target, 0o700); err != nil {
		return "", fmt.Errorf("create governed CODEX_HOME: %w", err)
	}
	_ = os.Remove(filepath.Join(target, "config.toml"))

	var hostCodex string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		hostCodex = filepath.Join(home, ".codex")
	}
	if hostCodex != "" {
		filesToMirror := []string{"auth.json", "version.json", "models_cache.json"}
		for _, fname := range filesToMirror {
			hostPath := filepath.Join(hostCodex, fname)
			targetPath := filepath.Join(target, fname)
			if info, err := os.Stat(hostPath); err == nil && !info.IsDir() {
				if data, err := os.ReadFile(hostPath); err == nil {
					_ = os.WriteFile(targetPath, data, 0o600)
				}
			}
		}
	}
	return target, nil
}

// RequireAppServerConfigFree enforces the explicit local-mode contract for
// app-server execution. Unlike `codex exec`, app-server has no
// --ignore-user-config flag. MARSHAL therefore refuses to start it while a
// user config.toml could inject hooks, sandbox settings, approval behavior, or
// provider configuration outside the reviewed task envelope. It never deletes
// config or credentials: the operator must remove/migrate config.toml before
// enabling this mode, while authentication material remains untouched.
func RequireAppServerConfigFree() error {
	// This explicit, process-local override exists only for a supervised
	// migration while app-server integration is being completed. It is never a
	// default, is not persisted, and must not be surfaced as a PASS security
	// state. Removing the variable restores the config-free fail-closed gate.
	allowHostConfig := os.Getenv("MARSHAL_CODEX_APP_SERVER_ALLOW_HOST_CONFIG") == "1"
	if allowHostConfig {
		return nil
	}
	var root string
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		root = home
	} else if governed, err := EnsureGovernedCodexHome(); err == nil && governed != "" {
		root = governed
	} else if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
		root = filepath.Join(userHome, ".codex")
	}
	if root != "" {
		config := filepath.Join(root, "config.toml")
		if _, err := os.Stat(config); err == nil {
			return fmt.Errorf("%w: Codex app-server requires a config-free CODEX_HOME; remove or migrate %s before enabling app-server mode", model.ErrPolicyDenied, config)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("%w: cannot verify Codex app-server configuration boundary", model.ErrUnavailable)
		}
	}
	return nil
}

func (s *AppServer) ResumeSession(ctx context.Context, threadID string, request adapter.Request) (AppServerSession, error) {
	if err := RequireAppServerConfigFree(); err != nil {
		return AppServerSession{}, err
	}
	params, err := appServerThreadResumeParams(threadID, request)
	if err != nil {
		return AppServerSession{}, err
	}
	response, err := s.call(ctx, "thread/resume", params)
	if err != nil {
		return AppServerSession{}, err
	}
	got, err := appServerSessionFromResponse(response, request.Worktree)
	if err != nil {
		return AppServerSession{}, err
	}
	if got.ThreadID != threadID {
		return AppServerSession{}, fmt.Errorf("%w: app-server resumed unexpected thread", model.ErrConflict)
	}
	return got, nil
}

// StartTurn starts exactly one fixed MARSHAL-governed turn on an already bound
// thread. It does not expose free-form prompts, commands, tool calls, config,
// plugin, or remote-control methods.
func (s *AppServer) StartTurn(ctx context.Context, session AppServerSession, request adapter.Request) (AppServerTurn, error) {
	if session.ThreadID == "" || !session.Persistent || session.Worktree != request.Worktree {
		return AppServerTurn{}, fmt.Errorf("%w: invalid Codex app-server session binding", model.ErrInvalid)
	}
	params, err := appServerTurnStartParams(session.ThreadID, request)
	if err != nil {
		return AppServerTurn{}, err
	}
	response, err := s.call(ctx, "turn/start", params)
	if err != nil {
		return AppServerTurn{}, err
	}
	var decoded struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil || decoded.Turn.ID == "" {
		return AppServerTurn{}, fmt.Errorf("%w: invalid Codex app-server turn response", model.ErrInvalid)
	}
	return AppServerTurn{ThreadID: session.ThreadID, TurnID: decoded.Turn.ID}, nil
}

// InterruptTurn is the only lifecycle mutation exposed besides start/resume.
// It binds cancellation to the exact native thread and turn IDs.
func (s *AppServer) InterruptTurn(ctx context.Context, turn AppServerTurn) error {
	if turn.ThreadID == "" || turn.TurnID == "" {
		return fmt.Errorf("%w: Codex app-server thread and turn IDs are required", model.ErrInvalid)
	}
	_, err := s.call(ctx, "turn/interrupt", map[string]any{"threadId": turn.ThreadID, "turnId": turn.TurnID})
	return err
}

// WaitTurn waits for the terminal notification of this exact turn. A
// server-initiated approval request is never auto-approved; the caller receives
// an explicit approval-required result and the connection stays paused.
func (s *AppServer) WaitTurn(ctx context.Context, turn AppServerTurn) (AppServerTurnResult, error) {
	if turn.ThreadID == "" || turn.TurnID == "" {
		return AppServerTurnResult{}, fmt.Errorf("%w: Codex app-server thread and turn IDs are required", model.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connection == nil {
		return AppServerTurnResult{}, fmt.Errorf("%w: Codex app-server connection is unavailable", model.ErrUnavailable)
	}
	result, err := s.connection.waitTurn(ctx, turn)
	if err != nil {
		var approvalRequired *AppServerApprovalRequiredError
		if !errors.As(err, &approvalRequired) {
			s.closeLocked()
		}
	}
	return result, err
}

// DeclineApproval is deliberately the sole response operation currently
// exposed by this low-level bridge. It binds to the original request stored on
// the active connection and cannot be forged from a changed command/digest.
// Action acceptance belongs to canonical Process 05 approval authority.
func (s *AppServer) DeclineApproval(ctx context.Context, request AppServerApprovalRequest) error {
	if s == nil {
		return fmt.Errorf("%w: Codex app-server connection is unavailable", model.ErrUnavailable)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connection == nil {
		return fmt.Errorf("%w: Codex app-server connection is unavailable", model.ErrUnavailable)
	}
	if err := s.connection.declineApproval(ctx, request); err != nil {
		s.closeLocked()
		return err
	}
	return nil
}

// ResolveApproval is the only accepting path exposed by this client.  It
// never trusts a caller-provided boolean: the supplied canonical authority
// must validate and consume the exact pending request first.  A refused,
// stale, expired, or unavailable authority causes a typed native decline.
func (s *AppServer) ResolveApproval(ctx context.Context, request AppServerApprovalRequest, authority AppServerApprovalAuthority) error {
	if s == nil || authority == nil {
		return fmt.Errorf("%w: canonical MARSHAL approval authority is required", model.ErrPolicyDenied)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connection == nil {
		return fmt.Errorf("%w: Codex app-server connection is unavailable", model.ErrUnavailable)
	}
	if err := s.connection.validatePendingApproval(request); err != nil {
		return err
	}
	if err := authority.ApproveAppServerRequest(ctx, request); err != nil {
		// A request that cannot be approved must never remain implicitly
		// pending while another operation is started on the same client.
		_ = s.connection.declineApproval(ctx, request)
		return err
	}
	if err := s.connection.acceptApproval(ctx, request); err != nil {
		s.closeLocked()
		return err
	}
	return nil
}

func appServerSessionFromResponse(raw json.RawMessage, worktree string) (AppServerSession, error) {
	var response struct {
		Thread struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionId"`
			Ephemeral bool   `json:"ephemeral"`
		} `json:"thread"`
		Model             string          `json:"model"`
		CWD               string          `json:"cwd"`
		ApprovalPolicy    string          `json:"approvalPolicy"`
		ApprovalsReviewer string          `json:"approvalsReviewer"`
		Sandbox           json.RawMessage `json:"sandbox"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return AppServerSession{}, fmt.Errorf("decode Codex app-server session: %w", err)
	}
	if response.Thread.ID == "" || response.Thread.Ephemeral || response.CWD != worktree {
		return AppServerSession{}, fmt.Errorf("%w: invalid Codex app-server session binding", model.ErrInvalid)
	}
	if response.ApprovalPolicy != "untrusted" || response.ApprovalsReviewer != "user" || !appServerWorkspaceWriteSandbox(response.Sandbox) {
		return AppServerSession{}, fmt.Errorf("%w: Codex app-server did not preserve local approval/sandbox policy (policy=%q reviewer=%q sandbox=%q)", model.ErrPolicyDenied, response.ApprovalPolicy, response.ApprovalsReviewer, boundedAppServerDisplay(response.Sandbox, 256))
	}
	return AppServerSession{ThreadID: response.Thread.ID, SessionID: response.Thread.SessionID, Model: response.Model, Worktree: worktree, Protocol: appServerProtocolVersion, Persistent: true}, nil
}

func appServerWorkspaceWriteSandbox(raw json.RawMessage) bool {
	var sandbox struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &sandbox) != nil {
		return false
	}
	return sandbox.Type == "workspaceWrite" || sandbox.Type == "workspace-write"
}

func boundedAppServerDisplay(value []byte, limit int) string {
	if len(value) > limit {
		value = value[:limit]
	}
	return strings.TrimSpace(string(value))
}

func (s *AppServer) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if s == nil || s.binary == "" {
		return nil, fmt.Errorf("%w: Codex app-server binary is required", model.ErrUnavailable)
	}
	switch method {
	case "thread/start", "thread/resume", "turn/start", "turn/interrupt":
	default:
		return nil, fmt.Errorf("%w: app-server method %q is not allowed", model.ErrPolicyDenied, method)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connectLocked(ctx); err != nil {
		return nil, err
	}
	response, err := s.connection.call(ctx, method, params)
	if err != nil {
		// A failed or timed-out RPC cannot safely be reused: an old response
		// could otherwise be associated with a subsequent request.
		s.closeLocked()
		return nil, err
	}
	return response, nil
}

// Close stops only this client-owned local stdio subprocess. It never starts,
// stops, or enables the optional Codex managed daemon/remote-control routes.
func (s *AppServer) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
	return nil
}

func (s *AppServer) connectLocked(ctx context.Context) error {
	if s.connection != nil {
		return nil
	}
	cmd := exec.Command(s.binary, "app-server", "--stdio")
	codexHome := s.codexHome
	if codexHome == "" {
		codexHome = strings.TrimSpace(os.Getenv("CODEX_HOME"))
	}
	if codexHome != "" {
		cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open Codex app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open Codex app-server stdout: %w", err)
	}
	var stderr boundedAppServerBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Codex app-server: %w", err)
	}
	connection := &appServerConnection{
		cmd: cmd, in: stdin, stream: appServerStream{in: stdin, out: bufio.NewReader(stdout)}, stderr: &stderr,
		pending: make(map[string]AppServerApprovalRequest),
	}
	if _, err := connection.call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "marshal", "title": "MARSHAL", "version": s.clientVersion},
		"capabilities": map[string]any{"experimentalApi": false, "requestAttestation": false},
	}); err != nil {
		connection.close()
		return fmt.Errorf("initialize Codex app-server: %w: %s", err, stderr.String())
	}
	s.connection = connection
	return nil
}

func (s *AppServer) closeLocked() {
	if s.connection != nil {
		s.connection.close()
		s.connection = nil
	}
}

type appServerConnection struct {
	cmd     *exec.Cmd
	in      io.Closer
	stream  appServerStream
	stderr  *boundedAppServerBuffer
	nextID  int
	pending map[string]AppServerApprovalRequest
}

func (c *appServerConnection) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.nextID++
	type callResult struct {
		response json.RawMessage
		err      error
	}
	done := make(chan callResult, 1)
	go func(id int) {
		response, err := c.stream.call(id, method, params)
		done <- callResult{response: response, err: err}
	}(c.nextID)
	select {
	case result := <-done:
		if result.err != nil {
			return nil, fmt.Errorf("app-server %s: %w: %s", method, result.err, c.stderr.String())
		}
		return result.response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *appServerConnection) close() {
	if c.in != nil {
		_ = c.in.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
}

func (c *appServerConnection) declineApproval(ctx context.Context, request AppServerApprovalRequest) error {
	return c.respondApproval(ctx, request, "decline")
}

func (c *appServerConnection) acceptApproval(ctx context.Context, request AppServerApprovalRequest) error {
	return c.respondApproval(ctx, request, "accept")
}

func (c *appServerConnection) respondApproval(ctx context.Context, request AppServerApprovalRequest, decision string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if decision != "accept" && decision != "decline" {
		return fmt.Errorf("%w: invalid Codex approval decision", model.ErrInvalid)
	}
	original, err := c.pendingApproval(request)
	if err != nil {
		return err
	}
	if err := c.stream.respond(original.wireID, map[string]any{"decision": decision}); err != nil {
		return err
	}
	delete(c.pending, request.RequestID)
	return nil
}

func (c *appServerConnection) validatePendingApproval(request AppServerApprovalRequest) error {
	_, err := c.pendingApproval(request)
	return err
}

func (c *appServerConnection) pendingApproval(request AppServerApprovalRequest) (AppServerApprovalRequest, error) {
	original, ok := c.pending[request.RequestID]
	if !ok || original.Digest == "" || original.Method != request.Method || original.ThreadID != request.ThreadID || original.TurnID != request.TurnID || original.ItemID != request.ItemID || original.ApprovalID != request.ApprovalID || original.Kind != request.Kind || original.Command != request.Command {
		return AppServerApprovalRequest{}, fmt.Errorf("%w: stale or mismatched Codex approval request", model.ErrConflict)
	}
	if original.Worktree == "" {
		// fileChange approvals may omit cwd. The only acceptable enrichment is
		// the deterministic durable worktree binding performed above; replace
		// the local pending copy with that exact bound digest while retaining
		// its original wire JSON-RPC ID for the response.
		bound, err := BindAppServerApprovalWorktree(original, request.Worktree)
		if err != nil || bound.Digest != request.Digest {
			return AppServerApprovalRequest{}, fmt.Errorf("%w: stale or mismatched Codex approval request", model.ErrConflict)
		}
		c.pending[request.RequestID] = bound
		original = bound
	}
	if original.Digest != request.Digest || original.Worktree != request.Worktree {
		return AppServerApprovalRequest{}, fmt.Errorf("%w: stale or mismatched Codex approval request", model.ErrConflict)
	}
	return original, nil
}

func (c *appServerConnection) waitTurn(ctx context.Context, expected AppServerTurn) (AppServerTurnResult, error) {
	type readResult struct {
		line []byte
		err  error
	}
	for {
		done := make(chan readResult, 1)
		go func() {
			line, err := c.stream.out.ReadBytes('\n')
			done <- readResult{line: line, err: err}
		}()
		select {
		case <-ctx.Done():
			return AppServerTurnResult{}, ctx.Err()
		case read := <-done:
			if read.err != nil {
				return AppServerTurnResult{}, read.err
			}
			var envelope struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(read.line), &envelope); err != nil {
				return AppServerTurnResult{}, fmt.Errorf("decode app-server notification: %w", err)
			}
			if len(envelope.ID) > 0 && envelope.Method != "" {
				approval, err := appServerApprovalFromRequest(envelope)
				if err != nil {
					return AppServerTurnResult{}, err
				}
				if c.pending == nil {
					c.pending = make(map[string]AppServerApprovalRequest)
				}
				c.pending[approval.RequestID] = approval
				return AppServerTurnResult{}, &AppServerApprovalRequiredError{Request: approval}
			}
			if envelope.Method != "turn/completed" {
				continue
			}
			var complete struct {
				ThreadID string `json:"threadId"`
				Turn     struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
					Items []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"items"`
				} `json:"turn"`
			}
			if err := json.Unmarshal(envelope.Params, &complete); err != nil {
				return AppServerTurnResult{}, fmt.Errorf("decode completed turn: %w", err)
			}
			// The app-server protocol defines turn/completed as {turn}; unlike
			// approval requests, threadId is optional here.  The turn ID is the
			// durable correlation key returned by turn/start.  Require a supplied
			// thread ID to match, but do not discard a protocol-valid terminal
			// event merely because it omits that redundant field.
			if (complete.ThreadID != "" && complete.ThreadID != expected.ThreadID) || complete.Turn.ID != expected.TurnID {
				continue
			}
			var finalText string
			for _, item := range complete.Turn.Items {
				if item.Type == "agentMessage" && item.Text != "" {
					finalText = item.Text
				}
			}
			var errMsg string
			if complete.Turn.Error != nil && complete.Turn.Error.Message != "" {
				errMsg = complete.Turn.Error.Message
			}
			return AppServerTurnResult{ThreadID: expected.ThreadID, TurnID: complete.Turn.ID, Status: complete.Turn.Status, FinalText: finalText, ErrorMessage: errMsg}, nil
		}
	}
}

func appServerApprovalFromRequest(envelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}) (AppServerApprovalRequest, error) {
	if envelope.Method != "item/commandExecution/requestApproval" && envelope.Method != "item/fileChange/requestApproval" {
		return AppServerApprovalRequest{}, fmt.Errorf("%w: unsupported Codex approval method %q", model.ErrPolicyDenied, envelope.Method)
	}
	var params struct {
		ThreadID   string `json:"threadId"`
		TurnID     string `json:"turnId"`
		ItemID     string `json:"itemId"`
		ApprovalID string `json:"approvalId"`
		Kind       string `json:"kind"`
		Command    string `json:"command"`
		CWD        string `json:"cwd"`
	}
	if err := json.Unmarshal(envelope.Params, &params); err != nil {
		return AppServerApprovalRequest{}, fmt.Errorf("decode Codex approval request: %w", err)
	}
	var requestID string
	if err := json.Unmarshal(envelope.ID, &requestID); err != nil {
		// Keep a stable display/digest key for numeric JSON-RPC IDs while the
		// private wireID below retains their original JSON type for response.
		requestID = string(envelope.ID)
	}
	if requestID == "" || params.ThreadID == "" || params.TurnID == "" || params.ItemID == "" {
		// Safe diagnostics only: the raw request, command, path, and payload
		// never leave the app-server connection. These presence bits let an
		// operator distinguish a protocol-version mismatch from a rejected
		// governed request without creating an evidence leak.
		return AppServerApprovalRequest{}, fmt.Errorf("%w: incomplete Codex approval binding (request_id=%t thread_id=%t turn_id=%t item_id=%t)", model.ErrInvalid, requestID != "", params.ThreadID != "", params.TurnID != "", params.ItemID != "")
	}
	approval := AppServerApprovalRequest{
		RequestID: requestID, Method: envelope.Method, ThreadID: params.ThreadID, TurnID: params.TurnID,
		ItemID: params.ItemID, ApprovalID: params.ApprovalID, Kind: params.Kind, Command: params.Command, Worktree: params.CWD,
		wireID: append(json.RawMessage(nil), envelope.ID...),
	}
	digest, err := appServerApprovalDigest(approval)
	if err != nil {
		return AppServerApprovalRequest{}, err
	}
	approval.Digest = digest
	return approval, nil
}

// BindAppServerApprovalWorktree binds an approval whose native protocol shape
// omits cwd (notably fileChange approvals) to the already durable Process 05
// task worktree. It never replaces a supplied, different worktree. The exact
// approval digest is recomputed after binding so the canonical queue cannot
// approve a request with an unbound or substituted scope.
func BindAppServerApprovalWorktree(request AppServerApprovalRequest, worktree string) (AppServerApprovalRequest, error) {
	if !filepath.IsAbs(worktree) || filepath.Clean(worktree) != worktree {
		return AppServerApprovalRequest{}, fmt.Errorf("%w: native approval worktree must be a clean absolute path", model.ErrInvalid)
	}
	if request.Worktree != "" && request.Worktree != worktree {
		return AppServerApprovalRequest{}, fmt.Errorf("%w: native approval worktree differs from the durable Process 05 binding", model.ErrConflict)
	}
	request.Worktree = worktree
	digest, err := appServerApprovalDigest(request)
	if err != nil {
		return AppServerApprovalRequest{}, err
	}
	request.Digest = digest
	return request, nil
}

func appServerApprovalDigest(approval AppServerApprovalRequest) (string, error) {
	binding, err := json.Marshal(struct {
		Method, ThreadID, TurnID, ItemID, ApprovalID, Kind, Command, Worktree string
	}{approval.Method, approval.ThreadID, approval.TurnID, approval.ItemID, approval.ApprovalID, approval.Kind, approval.Command, approval.Worktree})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(binding)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

type appServerStream struct {
	in  io.Writer
	out *bufio.Reader
}

func (s appServerStream) call(id int, method string, params any) (json.RawMessage, error) {
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if _, err := s.in.Write(append(encoded, '\n')); err != nil {
		return nil, err
	}
	for {
		line, err := s.out.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(line), &envelope); err != nil {
			return nil, fmt.Errorf("decode app-server JSON-RPC: %w", err)
		}
		var responseID int
		if len(envelope.ID) == 0 || json.Unmarshal(envelope.ID, &responseID) != nil || responseID != id {
			// Notifications and server-initiated requests cannot be treated as
			// implicit approval.  This narrow session-only client never starts a
			// turn, so it simply ignores notifications here.
			continue
		}
		if envelope.Error != nil {
			return nil, fmt.Errorf("app-server: %s", strings.TrimSpace(envelope.Error.Message))
		}
		return envelope.Result, nil
	}
}

func (s appServerStream) respond(id json.RawMessage, result any) error {
	if len(id) == 0 || !json.Valid(id) {
		return fmt.Errorf("%w: missing or invalid Codex JSON-RPC request ID", model.ErrInvalid)
	}
	encoded, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{JSONRPC: "2.0", ID: id, Result: result})
	if err != nil {
		return err
	}
	_, err = s.in.Write(append(encoded, '\n'))
	return err
}

type boundedAppServerBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedAppServerBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	original := len(value)
	const limit = 8 << 10
	if remaining := limit - len(b.data); remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		b.data = append(b.data, value...)
	}
	return original, nil
}

func (b *boundedAppServerBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.data))
}
