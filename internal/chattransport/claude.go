package chattransport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ClaudePermissionTool is the MARSHAL MCP tool Claude Code calls, through
// --permission-prompt-tool, whenever a tool call needs a decision. Its answer
// comes from the Board inbox; Claude never shows a prompt of its own.
const ClaudePermissionTool = "mcp__marshal__marshal_permission_prompt"

// ClaudeRange is the Claude Code version range whose stream-json input and
// output, --include-partial-messages and --permission-prompt-tool contract
// this transport was qualified against. Outside it the transport refuses
// before any turn is sent.
var ClaudeRange = Range{Min: "2.0.0", Below: "3.0.0"}

// Claude drives `claude -p --input-format stream-json --output-format
// stream-json`. One process carries the whole conversation: each operator
// turn is one JSON line on stdin. When the process ends, the next turn (or
// Resume) starts a new one with --resume, so the native session continues.
type Claude struct {
	runner Runner
	events chan Event
	perms  chan PermissionAsk
	asks   claudeAsks

	mu        sync.Mutex
	session   Session
	sessionID string
	proc      Process
	ctx       context.Context
	cancel    context.CancelFunc
	started   bool
	closed    bool
	gen       int // bumps per process, so a stale reader cannot clear a newer one

	writeMu sync.Mutex // serialises stdin writes without holding mu
}

// NewClaude returns an unstarted Claude transport.
func NewClaude(runner Runner) *Claude {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Claude{runner: runner, events: make(chan Event, 512), perms: make(chan PermissionAsk, 16)}
}

func (c *Claude) Events() <-chan Event              { return c.events }
func (c *Claude) Permissions() <-chan PermissionAsk { return c.perms }

// SessionID is the native session this transport continues.
func (c *Claude) SessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

// ClaudeArgs is the exact argument vector for one Claude process: the
// structured-mode flags, the resume target, then the caller's own launch
// arguments unchanged.
func ClaudeArgs(s Session, sessionID string) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-prompt-tool", ClaudePermissionTool,
	}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	return append(args, s.Args...)
}

// Start qualifies the installed version and starts the provider process.
func (c *Claude) Start(ctx context.Context, s Session) error {
	if strings.TrimSpace(s.Binary) == "" {
		return fmt.Errorf("%w: no Claude Code binary", ErrUnqualified)
	}
	out, err := c.runner.Output(ctx, s.Binary, "--version")
	if err != nil {
		return fmt.Errorf("%w: claude --version: %v", ErrUnqualified, err)
	}
	if err := ClaudeRange.Qualify(string(out)); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("chat transport is closed")
	}
	if c.started {
		return errors.New("chat transport already started")
	}
	c.session = s
	c.sessionID = s.SessionID
	c.ctx, c.cancel = context.WithCancel(context.WithoutCancel(ctx))
	c.started = true
	return c.spawnLocked()
}

func (c *Claude) spawnLocked() error {
	proc, err := c.runner.Start(c.ctx, c.session.Binary, c.session.Dir, ClaudeArgs(c.session, c.sessionID), c.session.Env)
	if err != nil {
		return fmt.Errorf("start Claude Code: %w", err)
	}
	c.gen++
	c.proc = proc
	go c.read(c.ctx, proc, c.gen)
	return nil
}

// Resume ends the current process and continues sessionID in a new one.
func (c *Claude) Resume(ctx context.Context, sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started || c.closed {
		return errors.New("chat transport is not running")
	}
	if c.proc != nil {
		_ = c.proc.Stdin().Close()
		_ = c.proc.Kill()
		c.proc = nil
	}
	c.sessionID = sessionID
	return c.spawnLocked()
}

// claudeUserLine is one stream-json input message.
type claudeUserLine struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// Send writes one operator turn. A process that has ended is resumed first.
func (c *Claude) Send(ctx context.Context, t Turn) error {
	if strings.TrimSpace(t.Text) == "" {
		return errors.New("empty message")
	}
	var line claudeUserLine
	line.Type = "user"
	line.Message.Role = "user"
	line.Message.Content = append(line.Message.Content, struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{"text", t.Text})
	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if !c.started || c.closed {
		c.mu.Unlock()
		return errors.New("chat transport is not running")
	}
	if c.proc == nil {
		if err := c.spawnLocked(); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	proc := c.proc
	c.mu.Unlock()
	// The write happens outside mu: a provider blocked on its own stdout
	// must still be drained by the reader, which needs no lock to emit.
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := proc.Stdin().Write(append(data, '\n')); err != nil {
		return fmt.Errorf("send to Claude Code: %w", err)
	}
	return nil
}

// Ask is called by the permission tool's broker for one tool call. It
// surfaces the ask once on Permissions and waits for Decide or ctx. A
// re-asked ID (the broker long-polls) joins the waiting entry.
func (c *Claude) Ask(ctx context.Context, ask PermissionAsk) (Decision, error) {
	if strings.TrimSpace(ask.ID) == "" {
		return Decision{}, errors.New("permission ask has no id")
	}
	ask.Provider = "claude"
	if ask.Summary == "" {
		ask.Summary = Summarize(ask.Input, 160)
	}
	ch, fresh := c.asks.register(ask.ID)
	if fresh {
		select {
		case c.perms <- ask:
		case <-ctx.Done():
			c.asks.drop(ask.ID)
			return Decision{}, ctx.Err()
		}
	}
	select {
	case d := <-ch:
		c.asks.drop(ask.ID)
		return d, nil
	case <-ctx.Done():
		return Decision{}, ctx.Err()
	}
}

// Decide answers a pending ask.
func (c *Claude) Decide(_ context.Context, ask PermissionAsk, d Decision) error {
	return c.asks.resolve(ask.ID, d)
}

// ClaudePermissionResult is the permission tool's answer in the shape
// Claude Code reads: allow with the unchanged input, or deny with a reason.
func ClaudePermissionResult(input json.RawMessage, d Decision) json.RawMessage {
	var out []byte
	if d.Allow {
		if len(input) == 0 || !json.Valid(input) {
			input = json.RawMessage("{}")
		}
		out, _ = json.Marshal(struct {
			Behavior     string          `json:"behavior"`
			UpdatedInput json.RawMessage `json:"updatedInput"`
		}{"allow", input})
	} else {
		msg := strings.TrimSpace(d.Message)
		if msg == "" {
			msg = "Denied by the operator in the MARSHAL inbox."
		}
		out, _ = json.Marshal(struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message"`
		}{"deny", msg})
	}
	return out
}

// Close stops the process and denies every unanswered ask.
func (c *Claude) Close() error {
	c.mu.Lock()
	c.closed = true
	proc := c.proc
	c.proc = nil
	cancel := c.cancel
	c.mu.Unlock()
	if proc != nil {
		_ = proc.Stdin().Close()
		_ = proc.Kill()
	}
	if cancel != nil {
		cancel()
	}
	c.asks.denyAll("The Marshal chat closed before the operator decided.")
	return nil
}

func (c *Claude) emit(ctx context.Context, e Event) {
	select {
	case c.events <- e:
	case <-ctx.Done():
	}
}

func (c *Claude) read(ctx context.Context, proc Process, gen int) {
	sc := bufio.NewScanner(proc.Stdout())
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var p claudeParser
	for sc.Scan() {
		for _, e := range p.parse(sc.Bytes()) {
			if e.Kind == EventSession && e.SessionID != "" {
				c.mu.Lock()
				if c.gen == gen {
					c.sessionID = e.SessionID
				}
				c.mu.Unlock()
			}
			c.emit(ctx, e)
		}
	}
	scanErr := sc.Err()
	waitErr := proc.Wait()
	c.mu.Lock()
	current := c.gen == gen
	if current {
		c.proc = nil
	}
	closed := c.closed
	c.mu.Unlock()
	if !current || closed {
		return
	}
	// The asks this process made can no longer be answered by it.
	c.asks.denyAll("The Marshal process ended before the operator decided.")
	text := "Claude Code exited"
	if err := errors.Join(scanErr, waitErr); err != nil {
		text += ": " + OneLine(err.Error(), 160)
	}
	c.emit(ctx, Event{Kind: EventExit, Text: text, IsError: scanErr != nil || waitErr != nil})
}

// claudeParser turns stream-json lines into Events. It keeps the message
// being streamed so deltas name the message they belong to.
type claudeParser struct{ message string }

type claudeLine struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Model     string          `json:"model"`
	IsError   bool            `json:"is_error"`
	Result    string          `json:"result"`
	CostUSD   *float64        `json:"total_cost_usd"`
	Event     json.RawMessage `json:"event"`
	Message   json.RawMessage `json:"message"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type claudeMessage struct {
	ID      string          `json:"id"`
	Content json.RawMessage `json:"content"`
}

func (p *claudeParser) parse(line []byte) []Event {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var l claudeLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	switch l.Type {
	case "system":
		if l.Subtype == "init" && l.SessionID != "" {
			return []Event{{Kind: EventSession, SessionID: l.SessionID, Model: l.Model}}
		}
	case "stream_event":
		var se struct {
			Type    string `json:"type"`
			Message struct {
				ID string `json:"id"`
			} `json:"message"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal(l.Event, &se) != nil {
			return nil
		}
		switch se.Type {
		case "message_start":
			p.message = se.Message.ID
		case "content_block_delta":
			if se.Delta.Type == "text_delta" && se.Delta.Text != "" {
				return []Event{{Kind: EventTextDelta, MessageID: p.message, Text: se.Delta.Text}}
			}
		}
	case "assistant":
		var m claudeMessage
		if json.Unmarshal(l.Message, &m) != nil {
			return nil
		}
		var out []Event
		for _, b := range blocks(m.Content) {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					out = append(out, Event{Kind: EventText, MessageID: m.ID, Text: b.Text})
				}
			case "tool_use":
				out = append(out, Event{Kind: EventToolUse, MessageID: m.ID, ToolID: b.ID, ToolName: b.Name, ToolInput: Summarize(b.Input, 160)})
			}
		}
		return out
	case "user":
		var m claudeMessage
		if json.Unmarshal(l.Message, &m) != nil {
			return nil
		}
		var out []Event
		for _, b := range blocks(m.Content) {
			if b.Type == "tool_result" {
				out = append(out, Event{Kind: EventToolResult, ToolID: b.ToolUseID, Text: OneLine(resultText(b.Content), 160), IsError: b.IsError})
			}
		}
		return out
	case "result":
		e := Event{Kind: EventResult, SessionID: l.SessionID, IsError: l.IsError || (l.Subtype != "" && l.Subtype != "success"), CostUSD: l.CostUSD}
		if e.IsError {
			e.Text = OneLine(l.Result, 240)
			if e.Text == "" {
				e.Text = l.Subtype
			}
		}
		return []Event{e}
	}
	return nil
}

func blocks(raw json.RawMessage) []claudeBlock {
	var bs []claudeBlock
	if json.Unmarshal(raw, &bs) == nil {
		return bs
	}
	return nil
}

// resultText reads a tool_result content, which is a string or a list of
// text blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []string
	for _, b := range blocks(raw) {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " ")
}

// claudeAsks is the pending-ask table. A decision made while nobody waits
// (the broker is between long-polls) stays buffered for the next Ask.
type claudeAsks struct {
	mu      sync.Mutex
	entries map[string]*askEntry
}

type askEntry struct {
	ch       chan Decision
	resolved bool
}

func (a *claudeAsks) register(id string) (chan Decision, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.entries == nil {
		a.entries = map[string]*askEntry{}
	}
	if e, ok := a.entries[id]; ok {
		return e.ch, false
	}
	e := &askEntry{ch: make(chan Decision, 1)}
	a.entries[id] = e
	return e.ch, true
}

func (a *claudeAsks) drop(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.entries, id)
}

func (a *claudeAsks) resolve(id string, d Decision) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.entries[id]
	if !ok || e.resolved {
		return fmt.Errorf("permission ask %q is not pending", id)
	}
	e.resolved = true
	e.ch <- d
	return nil
}

func (a *claudeAsks) denyAll(msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.entries {
		if !e.resolved {
			e.resolved = true
			e.ch <- Decision{Message: msg}
		}
	}
}

var _ Transport = (*Claude)(nil)
