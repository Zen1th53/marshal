package claude

// This file exposes a deliberately small, typed subset of the Claude Code
// stream-json protocol. Claude Code has no app-server RPC surface; its
// governed lifecycle is a `--print --output-format stream-json` process whose
// stdout is a JSONL event stream and whose permission requests arrive through
// an out-of-band handler. As with the Codex app-server client, this file is
// not a general CLI passthrough: Process 05 remains the sole authority for
// tool execution, approvals, sandboxing, evidence, and cancellation.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

// streamProtocolVersion tracks the event vocabulary this client understands.
const streamProtocolVersion = "stream-json/v1"

// StreamSession is the narrow durable-session reference MARSHAL may retain.
// It never includes the host configuration directory, credentials, or raw
// protocol payload.
type StreamSession struct {
	SessionID  string
	Model      string
	Worktree   string
	Protocol   string
	Version    string
	Persistent bool
}

// StreamTurn is a typed reference to one Claude turn. The execution bridge may
// store these IDs as evidence, but never raw protocol messages.
type StreamTurn struct {
	SessionID string
	TurnID    string
}

// StreamTurnResult is a redacted, typed projection of terminal turn state.
type StreamTurnResult struct {
	SessionID    string
	TurnID       string
	Status       string
	FinalText    string
	ErrorMessage string
	Usage        adapter.Usage
}

// StreamApprovalRequest is an exact, digest-bound permission request emitted
// by Claude. It is only an observation: a separate canonical MARSHAL authority
// must make the decision. This type has no accepting method of its own.
type StreamApprovalRequest struct {
	RequestID string
	Method    string
	SessionID string
	TurnID    string
	ToolName  string
	Command   string
	Worktree  string
	Digest    string
}

type StreamApprovalRequiredError struct{ Request StreamApprovalRequest }

func (e *StreamApprovalRequiredError) Error() string {
	return fmt.Sprintf("%s: Claude requested permission for %s", model.ErrPolicyDenied, e.Request.ToolName)
}

func (e *StreamApprovalRequiredError) Unwrap() error { return model.ErrPolicyDenied }

// StreamApprovalAuthority is implemented only by the canonical MARSHAL
// execution bridge. The stream client never receives a boolean or an arbitrary
// "accept" option from a UI caller: it asks this authority to validate and
// consume the exact native request before it allows the tool to proceed.
type StreamApprovalAuthority interface {
	ApproveStreamRequest(context.Context, StreamApprovalRequest) error
}

// BindStreamApprovalWorktree rebinds a request to the exact worktree the
// governed task owns and recomputes its digest. A request whose own worktree
// disagrees is refused rather than silently re-homed, because an approval for
// one workspace must never authorize a tool call in another.
func BindStreamApprovalWorktree(request StreamApprovalRequest, worktree string) (StreamApprovalRequest, error) {
	if !filepath.IsAbs(worktree) || filepath.Clean(worktree) != worktree {
		return StreamApprovalRequest{}, fmt.Errorf("%w: Claude approval worktree must be a clean absolute path", model.ErrInvalid)
	}
	if request.Worktree != "" && request.Worktree != worktree {
		return StreamApprovalRequest{}, fmt.Errorf("%w: Claude approval worktree %q does not match the governed task worktree", model.ErrConflict, request.Worktree)
	}
	request.Worktree = worktree
	digest, err := streamApprovalDigest(request)
	if err != nil {
		return StreamApprovalRequest{}, err
	}
	request.Digest = digest
	return request, nil
}

// streamApprovalDigest binds every field that identifies the exact request, so
// an approval cannot be replayed against a changed tool call.
func streamApprovalDigest(approval StreamApprovalRequest) (string, error) {
	payload, err := json.Marshal([]string{
		approval.RequestID, approval.Method, approval.SessionID,
		approval.TurnID, approval.ToolName, approval.Command, approval.Worktree,
	})
	if err != nil {
		return "", fmt.Errorf("encode Claude approval digest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// streamTurnArgs builds the exact argument vector for one governed turn.
//
// Every boundary is re-injected on each turn rather than relied upon from an
// earlier one: a resumed session must not inherit a weaker permission mode or
// a wider writable root than the current constraint package allows.
func streamTurnArgs(session StreamSession, request adapter.Request) ([]string, error) {
	if request.TaskID == "" || request.Title == "" || request.Worktree == "" {
		return nil, fmt.Errorf("%w: incomplete Claude session request", model.ErrInvalid)
	}
	if !filepath.IsAbs(request.Worktree) || filepath.Clean(request.Worktree) != request.Worktree {
		return nil, fmt.Errorf("%w: Claude worktree must be a clean absolute path", model.ErrInvalid)
	}
	if err := ValidateDangerousFlags([]string{request.TaskID, request.Title, request.Worktree, request.Model}); err != nil {
		return nil, err
	}
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--input-format", "text",
		"--verbose",
		// MARSHAL owns approval. "manual" keeps Claude from auto-accepting an
		// edit, and "none" makes anything that would prompt fail closed rather
		// than block a non-interactive governed run forever.
		"--permission-mode", "manual",
		"--permission-prompts", "none",
		// Host customizations are not part of the governed contract: settings
		// sources, MCP servers, and plugin hooks could all widen what a task
		// may do without appearing in the constraint package.
		"--strict-mcp-config",
		"--setting-sources", "",
		// File tools stay inside the task worktree.
		"--add-dir", request.Worktree,
	}
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	if session.SessionID != "" {
		// Continuing the exact prior conversation; a fork would silently
		// abandon the durable session this task is bound to.
		args = append(args, "--resume", session.SessionID)
	}
	if err := ValidateDangerousFlags(args); err != nil {
		return nil, err
	}
	return args, nil
}

// StreamClient owns one local Claude Code process per governed turn.
type StreamClient struct {
	binary        string
	clientVersion string

	mu      sync.Mutex
	conn    *streamConnection
	session StreamSession
}

func NewStreamClient(binary, clientVersion string) *StreamClient {
	if binary == "" {
		return nil
	}
	return &StreamClient{binary: binary, clientVersion: clientVersion}
}

// StartSession records the typed session identity. No process is started until
// a turn begins: Claude Code has no idle session daemon, so a session that
// never runs a turn must not leave an orphaned process behind.
func (c *StreamClient) StartSession(ctx context.Context, request adapter.Request) (StreamSession, error) {
	if c == nil || c.binary == "" {
		return StreamSession{}, fmt.Errorf("%w: Claude stream client is unavailable", model.ErrUnavailable)
	}
	if _, err := streamTurnArgs(StreamSession{}, request); err != nil {
		return StreamSession{}, err
	}
	session := StreamSession{
		Model: request.Model, Worktree: request.Worktree,
		Protocol: streamProtocolVersion, Version: c.clientVersion, Persistent: true,
	}
	c.mu.Lock()
	c.session = session
	c.mu.Unlock()
	return session, nil
}

// ResumeSession rebinds an existing native session ID to this client.
func (c *StreamClient) ResumeSession(ctx context.Context, sessionID string, request adapter.Request) (StreamSession, error) {
	if strings.TrimSpace(sessionID) == "" {
		return StreamSession{}, fmt.Errorf("%w: Claude session ID is required", model.ErrInvalid)
	}
	if !isNativeIdentifier(sessionID) {
		return StreamSession{}, fmt.Errorf("%w: Claude session ID is not a native identifier", model.ErrInvalid)
	}
	session, err := c.StartSession(ctx, request)
	if err != nil {
		return StreamSession{}, err
	}
	session.SessionID = sessionID
	c.mu.Lock()
	c.session = session
	c.mu.Unlock()
	return session, nil
}

// StartTurn launches the governed process for one turn and returns as soon as
// the native session identity is known, so the caller can persist the binding
// before waiting for a terminal event.
func (c *StreamClient) StartTurn(ctx context.Context, session StreamSession, request adapter.Request) (StreamTurn, error) {
	if c == nil || c.binary == "" {
		return StreamTurn{}, fmt.Errorf("%w: Claude stream client is unavailable", model.ErrUnavailable)
	}
	args, err := streamTurnArgs(session, request)
	if err != nil {
		return StreamTurn{}, err
	}
	governedHome, err := EnsureGovernedClaudeHome()
	if err != nil {
		return StreamTurn{}, err
	}
	prompt, err := buildPrompt(request)
	if err != nil {
		return StreamTurn{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return StreamTurn{}, fmt.Errorf("%w: a Claude turn is already live on this client", model.ErrConflict)
	}

	cmd := exec.CommandContext(ctx, c.binary, args...)
	cmd.Dir = request.Worktree
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+governedHome)
	cmd.Stdin = bytes.NewReader(append(prompt, '\n'))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return StreamTurn{}, fmt.Errorf("open Claude stdout: %w", err)
	}
	stderr := &boundedStreamBuffer{limit: 64 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return StreamTurn{}, fmt.Errorf("%w: start Claude process: %v", model.ErrUnavailable, err)
	}

	conn := &streamConnection{cmd: cmd, stderr: stderr, scanner: bufio.NewScanner(stdout)}
	conn.scanner.Buffer(make([]byte, 64<<10), 8<<20)

	// Read up to the init event so the native session ID is durable before any
	// tool can run. Without it a crash would leave a live session MARSHAL
	// cannot name, cancel, or resume.
	initEvent, err := conn.readInit()
	if err != nil {
		conn.close()
		return StreamTurn{}, err
	}
	turnID := initEvent.UUID
	if turnID == "" {
		turnID = initEvent.SessionID
	}
	c.conn = conn
	c.session = StreamSession{
		SessionID: initEvent.SessionID, Model: initEvent.Model,
		Worktree: request.Worktree, Protocol: streamProtocolVersion,
		Version: initEvent.Version, Persistent: true,
	}
	return StreamTurn{SessionID: initEvent.SessionID, TurnID: turnID}, nil
}

// InterruptTurn cancels the live turn. Claude Code exposes no interrupt RPC on
// this surface, so the governed process is signalled directly.
func (c *StreamClient) InterruptTurn(ctx context.Context, turn StreamTurn) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("%w: no live Claude turn to interrupt", model.ErrUnavailable)
	}
	return conn.interrupt()
}

// WaitTurn reads until the turn reaches a terminal state or asks permission.
// A permission request is returned as StreamApprovalRequiredError so the
// caller pauses at the exact tool call rather than deciding here.
func (c *StreamClient) WaitTurn(ctx context.Context, turn StreamTurn) (StreamTurnResult, error) {
	c.mu.Lock()
	conn := c.conn
	worktree := c.session.Worktree
	c.mu.Unlock()
	if conn == nil {
		return StreamTurnResult{}, fmt.Errorf("%w: no live Claude turn to wait on", model.ErrUnavailable)
	}
	return conn.waitTurn(ctx, turn, worktree)
}

// DeclineApproval refuses a pending request. Because this surface fails closed
// on any prompt, a declined request means the turn has already been denied by
// the CLI; terminating the process is what makes that refusal durable.
func (c *StreamClient) DeclineApproval(ctx context.Context, request StreamApprovalRequest) error {
	if request.Digest == "" {
		return fmt.Errorf("%w: Claude approval request is not digest-bound", model.ErrInvalid)
	}
	return c.Close()
}

// ResolveApproval consumes the exact canonical approval before the tool call
// is allowed to proceed. The authority, not this client, decides.
func (c *StreamClient) ResolveApproval(ctx context.Context, request StreamApprovalRequest, authority StreamApprovalAuthority) error {
	if authority == nil {
		return fmt.Errorf("%w: Claude approval authority is required", model.ErrPolicyDenied)
	}
	if request.Digest == "" {
		return fmt.Errorf("%w: Claude approval request is not digest-bound", model.ErrInvalid)
	}
	expected, err := streamApprovalDigest(request)
	if err != nil {
		return err
	}
	if expected != request.Digest {
		return fmt.Errorf("%w: Claude approval digest does not match its request", model.ErrConflict)
	}
	return authority.ApproveStreamRequest(ctx, request)
}

func (c *StreamClient) Close() error {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil {
		conn.close()
	}
	return nil
}

type streamConnection struct {
	cmd     *exec.Cmd
	stderr  *boundedStreamBuffer
	scanner *bufio.Scanner
}

type streamInitEvent struct {
	SessionID string
	Model     string
	Version   string
	UUID      string
}

func (c *streamConnection) readInit() (streamInitEvent, error) {
	for c.scanner.Scan() {
		line := strings.TrimSpace(c.scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			SessionID string `json:"session_id"`
			Model     string `json:"model"`
			Version   string `json:"claude_code_version"`
			UUID      string `json:"uuid"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event.Type == "system" && event.Subtype == "init" {
			if !isNativeIdentifier(event.SessionID) {
				return streamInitEvent{}, fmt.Errorf("%w: Claude reported an unusable session identifier", model.ErrUnavailable)
			}
			return streamInitEvent{
				SessionID: event.SessionID, Model: event.Model,
				Version: event.Version, UUID: event.UUID,
			}, nil
		}
	}
	if err := c.scanner.Err(); err != nil {
		return streamInitEvent{}, fmt.Errorf("%w: read Claude init event: %v", model.ErrUnavailable, err)
	}
	return streamInitEvent{}, fmt.Errorf("%w: Claude exited before reporting a session: %s",
		model.ErrUnavailable, boundedStreamDisplay(c.stderr.Bytes(), 512))
}

func (c *streamConnection) waitTurn(ctx context.Context, expected StreamTurn, worktree string) (StreamTurnResult, error) {
	result := StreamTurnResult{SessionID: expected.SessionID, TurnID: expected.TurnID}
	for c.scanner.Scan() {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		line := strings.TrimSpace(c.scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event streamEvent
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		// A permission denial is the native signal that a tool call needs an
		// authority decision. It is surfaced as an exact, digest-bound pause.
		if approval, ok := streamApprovalFromEvent(event, expected, worktree); ok {
			bound, err := BindStreamApprovalWorktree(approval, worktree)
			if err != nil {
				return result, err
			}
			return result, &StreamApprovalRequiredError{Request: bound}
		}
		if event.Type == "result" {
			result.Status = "completed"
			if event.Subtype != "success" || event.IsError {
				result.Status = "failed"
				result.ErrorMessage = strings.TrimSpace(event.Result)
				if result.ErrorMessage == "" {
					result.ErrorMessage = event.Subtype
				}
			}
			result.FinalText = event.Result
			result.Usage = streamUsage(event)
			return result, nil
		}
	}
	if err := c.scanner.Err(); err != nil {
		return result, fmt.Errorf("%w: read Claude event stream: %v", model.ErrUnavailable, err)
	}
	return result, fmt.Errorf("%w: Claude stream ended before a terminal result: %s",
		model.ErrUnavailable, boundedStreamDisplay(c.stderr.Bytes(), 512))
}

type streamEvent struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	SessionID        string          `json:"session_id"`
	UUID             string          `json:"uuid"`
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	DurationMs       int64           `json:"duration_ms"`
	TotalCostUSD     *float64        `json:"total_cost_usd"`
	NumTurns         int             `json:"num_turns"`
	Usage            *streamUsageRaw `json:"usage"`
	PermissionDenies []struct {
		ToolName  string          `json:"tool_name"`
		ToolUseID string          `json:"tool_use_id"`
		ToolInput json.RawMessage `json:"tool_input"`
	} `json:"permission_denials"`
}

type streamUsageRaw struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
}

// streamUsage normalizes reported consumption. Unknown values stay unknown:
// a missing field is never synthesized into a zero, because a fabricated zero
// would understate real spend in evidence and budget decisions.
func streamUsage(event streamEvent) adapter.Usage {
	usage := adapter.Usage{ModelCalls: event.NumTurns, DurationMs: event.DurationMs}
	if event.TotalCostUSD != nil {
		usage.Reported = true
		cost := *event.TotalCostUSD
		usage.CostUSD = &cost
	}
	if event.Usage == nil {
		return usage
	}
	usage.Reported = true
	if event.Usage.InputTokens != nil {
		prompt := *event.Usage.InputTokens
		// Cache reads and cache writes are real prompt-side tokens; omitting
		// them reports a fraction of what the turn actually consumed.
		if event.Usage.CacheReadInputTokens != nil {
			prompt += *event.Usage.CacheReadInputTokens
		}
		if event.Usage.CacheCreationInputTokens != nil {
			prompt += *event.Usage.CacheCreationInputTokens
		}
		usage.PromptTokens = &prompt
	}
	if event.Usage.OutputTokens != nil {
		completion := *event.Usage.OutputTokens
		usage.CompletionTokens = &completion
	}
	if usage.PromptTokens != nil && usage.CompletionTokens != nil {
		total := *usage.PromptTokens + *usage.CompletionTokens
		usage.TotalTokens = &total
	}
	return usage
}

// streamApprovalFromEvent extracts an exact permission request. Only a native
// denial carries a tool identity; anything else is left to the terminal path.
func streamApprovalFromEvent(event streamEvent, turn StreamTurn, worktree string) (StreamApprovalRequest, bool) {
	if len(event.PermissionDenies) == 0 {
		return StreamApprovalRequest{}, false
	}
	denial := event.PermissionDenies[0]
	if strings.TrimSpace(denial.ToolName) == "" {
		return StreamApprovalRequest{}, false
	}
	requestID := denial.ToolUseID
	if requestID == "" {
		requestID = event.UUID
	}
	return StreamApprovalRequest{
		RequestID: requestID,
		Method:    "tool.permission",
		SessionID: turn.SessionID,
		TurnID:    turn.TurnID,
		ToolName:  denial.ToolName,
		// The raw tool input is bounded before it is carried anywhere: it is
		// provider-authored text that ends up in an operator-facing prompt.
		Command:  boundedStreamDisplay(denial.ToolInput, 2048),
		Worktree: worktree,
	}, true
}

func (c *streamConnection) interrupt() error {
	if c.cmd == nil || c.cmd.Process == nil {
		return fmt.Errorf("%w: Claude process is not running", model.ErrUnavailable)
	}
	if err := c.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("interrupt Claude process: %w", err)
	}
	return nil
}

func (c *streamConnection) close() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
}

func boundedStreamDisplay(value []byte, limit int) string {
	trimmed := strings.TrimSpace(string(value))
	if len(trimmed) <= limit {
		return trimmed
	}
	return trimmed[:limit] + "…"
}

// boundedStreamBuffer keeps provider stderr from growing without bound while
// still preserving the head, which is where a startup failure is reported.
type boundedStreamBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (b *boundedStreamBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if remaining := b.limit - b.buf.Len(); remaining > 0 {
		if len(value) > remaining {
			b.buf.Write(value[:remaining])
		} else {
			b.buf.Write(value)
		}
	}
	return len(value), nil
}

func (b *boundedStreamBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}
