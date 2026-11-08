// Package chattransport drives the Marshal model through a provider's
// structured mode so the Board can render the conversation in its own
// widget (TUI3-0013). The provider binary is launched with flags only: no
// patching, no wrapper that alters its behaviour, no screen scraping. The
// widget renders Events and never parses terminal text.
//
// A provider permission asked during a turn is surfaced on Permissions and
// is answered only through Decide, which the caller wires to the Board inbox
// and its canonical decision path. Nothing here decides on its own.
package chattransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// EventKind names one structured event of a conversation.
type EventKind string

const (
	// EventSession reports the native session identity (and model) once the
	// provider has started a turn.
	EventSession EventKind = "session"
	// EventTextDelta is a streamed fragment of an assistant message.
	EventTextDelta EventKind = "text_delta"
	// EventText is one complete assistant text block. It supersedes the
	// deltas of the same message.
	EventText EventKind = "text"
	// EventToolUse is a tool call the model made.
	EventToolUse EventKind = "tool_use"
	// EventToolResult is the result of an earlier tool call.
	EventToolResult EventKind = "tool_result"
	// EventResult ends a turn.
	EventResult EventKind = "result"
	// EventError is a transport or provider failure the operator should see.
	EventError EventKind = "error"
	// EventExit reports that the provider process ended.
	EventExit EventKind = "exit"
)

// Event is one structured conversation event. Text fields are provider
// authored and untrusted: renderers must sanitise them.
type Event struct {
	Kind      EventKind
	SessionID string
	Model     string
	MessageID string // assistant message the text belongs to
	Text      string
	ToolID    string
	ToolName  string
	ToolInput string // bounded, single-line summary of the tool input
	IsError   bool
	CostUSD   *float64
}

// PermissionAsk is a provider asking whether a tool call may run. ID is
// stable for one tool call, so a re-asked call joins its pending entry.
type PermissionAsk struct {
	ID       string
	Provider string
	ToolName string
	Input    json.RawMessage
	Summary  string // bounded, single-line input summary for the inbox
}

// Decision answers a PermissionAsk.
type Decision struct {
	Allow   bool
	Message string // shown to the model on deny
}

// Session is how a transport launches its provider. Args are the provider's
// own launch arguments (briefing, MCP configuration, model); the transport
// appends only the flags of its structured mode.
type Session struct {
	Binary    string
	Dir       string
	Args      []string
	Env       []string
	SessionID string // resume this native session when set
}

// Turn is one operator message.
type Turn struct{ Text string }

// Transport is one provider's structured conversation mode.
type Transport interface {
	Start(ctx context.Context, s Session) error
	Send(ctx context.Context, t Turn) error
	Events() <-chan Event
	Permissions() <-chan PermissionAsk
	Decide(ctx context.Context, ask PermissionAsk, d Decision) error
	Resume(ctx context.Context, sessionID string) error
	SessionID() string
	Close() error
}

// ErrUnqualified refuses a provider (or a provider version) whose structured
// mode is not qualified. The caller falls back to the tmux-docked chat.
var ErrUnqualified = errors.New("structured chat transport is not qualified")

// Process is one running provider process with piped stdio.
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Wait() error
	Kill() error
}

// Runner starts provider processes. Tests replace it with a fake; the real
// runner is exec.
type Runner interface {
	Start(ctx context.Context, binary, dir string, args, env []string) (Process, error)
	Output(ctx context.Context, binary string, args ...string) ([]byte, error)
}

// ExecRunner runs real processes.
type ExecRunner struct{}

type execProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
}

func (p *execProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *execProcess) Stdout() io.Reader     { return p.stdout }
func (p *execProcess) Wait() error           { return p.cmd.Wait() }
func (p *execProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

func (ExecRunner) Start(ctx context.Context, binary, dir string, args, env []string) (Process, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// Provider diagnostics never reach the Board's terminal.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProcess{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func (ExecRunner) Output(ctx context.Context, binary string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, binary, args...).Output()
}

// New returns the structured transport for a Marshal provider, or
// ErrUnqualified when that provider has none MARSHAL qualifies yet.
func New(provider string, runner Runner) (Transport, error) {
	if runner == nil {
		runner = ExecRunner{}
	}
	switch provider {
	case "claude":
		return NewClaude(runner), nil
	default:
		// Codex's adapter exposes app-server only for governed worker turns
		// (config-free CODEX_HOME, no streamed deltas); OpenCode serve and
		// ACP are not wired. All of them keep the tmux-docked chat.
		return nil, fmt.Errorf("%w: %s has no qualified structured chat mode; the tmux-docked chat stays in use", ErrUnqualified, provider)
	}
}

// version is a dotted numeric version.
type version [3]int

func parseVersion(s string) (version, bool) {
	var v version
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 {
		return v, false
	}
	parts := strings.SplitN(strings.TrimPrefix(fields[0], "v"), ".", 3)
	if len(parts) < 2 {
		return v, false
	}
	for i, p := range parts {
		// Pre-release and build suffixes do not change the range.
		if j := strings.IndexAny(p, "-+"); j >= 0 {
			p = p[:j]
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func (v version) less(o version) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] < o[i]
		}
	}
	return false
}

func (v version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

// Range is a qualified [Min, Below) version range.
type Range struct{ Min, Below string }

// Qualify reports whether reported (a `--version` line) lies in r.
func (r Range) Qualify(reported string) error {
	v, ok := parseVersion(reported)
	if !ok {
		return fmt.Errorf("%w: unreadable version %q", ErrUnqualified, strings.TrimSpace(reported))
	}
	lo, _ := parseVersion(r.Min)
	hi, _ := parseVersion(r.Below)
	if v.less(lo) || !v.less(hi) {
		return fmt.Errorf("%w: version %s is outside the qualified range >=%s <%s", ErrUnqualified, v, r.Min, r.Below)
	}
	return nil
}

// Summarize bounds a raw JSON tool input to one printable line.
func Summarize(raw json.RawMessage, limit int) string {
	var obj map[string]any
	text := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &obj) == nil {
		// The fields that name what a tool call does, most telling first.
		for _, key := range []string{"command", "file_path", "path", "pattern", "url", "query", "description", "prompt"} {
			if v, ok := obj[key].(string); ok && strings.TrimSpace(v) != "" {
				text = v
				break
			}
		}
	}
	return OneLine(text, limit)
}

// OneLine collapses whitespace and control characters and bounds s to limit
// runes.
func OneLine(s string, limit int) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == ' ' {
			if !space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if runes := []rune(out); limit > 0 && len(runes) > limit {
		out = string(runes[:max(0, limit-1)]) + "…"
	}
	return out
}

// Probe qualifies a provider binary without starting a conversation, so a
// caller can fall back before preparing anything for the structured mode.
func Probe(ctx context.Context, provider, binary string, runner Runner) error {
	if runner == nil {
		runner = ExecRunner{}
	}
	if provider != "claude" {
		_, err := New(provider, runner)
		return err
	}
	out, err := runner.Output(ctx, binary, "--version")
	if err != nil {
		return fmt.Errorf("%w: claude --version: %v", ErrUnqualified, err)
	}
	return ClaudeRange.Qualify(string(out))
}
