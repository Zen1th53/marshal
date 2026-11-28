package chattransport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
)

// Codex uses the exec JSON event contract, version 1. Each turn resumes
// the thread emitted by the preceding turn. Capability is checked locally.
type Codex struct {
	*Claude
	turnMu sync.Mutex
}

func NewCodex(r Runner) *Codex { return &Codex{Claude: NewClaude(r)} }
func probeCodex(ctx context.Context, binary string, r Runner) error {
	for _, args := range [][]string{{"exec", "--help"}, {"exec", "resume", "--help"}} {
		out, err := r.Output(ctx, binary, args...)
		if err != nil || !strings.Contains(string(out), "--json") {
			return fmt.Errorf("%w: exec JSON and resume JSON support required", ErrUnqualified)
		}
	}
	return nil
}
func (c *Codex) Start(ctx context.Context, s Session) error {
	if err := probeCodex(ctx, s.Binary, c.runner); err != nil {
		return err
	}
	// Never silently remove confinement or model options. Unsupported launch
	// options cause qualification failure and visible paste fallback.
	help, err := c.runner.Output(ctx, s.Binary, "exec", "--help")
	if err != nil {
		return fmt.Errorf("%w: exec help: %v", ErrUnqualified, err)
	}
	for _, arg := range s.Args {
		if strings.HasPrefix(arg, "-") {
			flag := strings.SplitN(arg, "=", 2)[0]
			if !strings.Contains(string(help), flag) {
				return fmt.Errorf("%w: exec does not advertise launch option %s", ErrUnqualified, flag)
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started || c.closed {
		return errors.New("chat transport already started or closed")
	}
	c.session = s
	c.sessionID = s.SessionID
	c.ctx, c.cancel = context.WithCancel(context.WithoutCancel(ctx))
	c.started = true
	return nil
}
func (c *Codex) Send(ctx context.Context, t Turn) error {
	if strings.TrimSpace(t.Text) == "" {
		return errors.New("empty message")
	}
	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	c.mu.Lock()
	if c.closed || !c.started {
		c.mu.Unlock()
		return errors.New("chat transport is not running")
	}
	if c.proc != nil {
		c.mu.Unlock()
		return errors.New("previous turn is still running")
	}
	// Parent exec options precede resume: its subcommand accepts fewer flags.
	args := append([]string{"exec"}, c.session.Args...)
	if c.sessionID != "" {
		args = append(args, "resume", "--json", c.sessionID)
	} else {
		args = append(args, "--json")
	}
	args = append(args, "-")
	proc, err := c.runner.Start(c.ctx, c.session.Binary, c.session.Dir, args, c.session.Env)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	c.proc = proc
	c.mu.Unlock()
	go c.readCodex(proc, t.ID)
	_, err = proc.Stdin().Write([]byte(t.Text))
	_ = proc.Stdin().Close()
	return err
}
func (c *Codex) readCodex(proc Process, id string) {
	sc := bufio.NewScanner(proc.Stdout())
	sc.Buffer(make([]byte, 65536), 16<<20)
	var terminal *Event
	for sc.Scan() {
		for _, e := range parseCodexV1(sc.Bytes()) {
			e.TurnID = id
			// Exec item IDs restart at zero in each process. Namespace them
			// by the durable owner turn so a later reply cannot replace an
			// earlier reply in the chat widget.
			if e.MessageID != "" {
				e.MessageID = id + ":" + e.MessageID
			}
			if e.SessionID != "" {
				c.mu.Lock()
				c.sessionID = e.SessionID
				c.mu.Unlock()
			}
			if e.Kind == EventResult {
				terminal = &e
			} else {
				c.emit(c.ctx, e)
			}
		}
	}
	if sc.Err() != nil {
		_ = proc.Kill()
	}
	err := errors.Join(sc.Err(), proc.Wait())
	c.mu.Lock()
	c.proc = nil
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	if terminal != nil {
		c.emit(c.ctx, *terminal)
	}
	if err != nil || terminal == nil {
		text := "Codex exec ended without a result"
		if err != nil {
			text += ": " + err.Error()
		}
		c.emit(c.ctx, Event{Kind: EventExit, TurnID: id, Text: text, IsError: true})
	}
}
func (c *Codex) Resume(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc != nil {
		return errors.New("cannot resume during a turn")
	}
	c.sessionID = id
	return nil
}
func parseCodexV1(data []byte) []Event {
	var v struct {
		Type    string `json:"type"`
		Thread  string `json:"thread_id"`
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
		Item struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		log.Printf("codex events v1: malformed event")
		return nil
	}
	switch v.Type {
	case "thread.started":
		return []Event{{Kind: EventSession, SessionID: v.Thread}}
	case "item.completed":
		if v.Item.Type == "agent_message" {
			return []Event{{Kind: EventText, MessageID: v.Item.ID, Text: v.Item.Text}}
		}
		switch v.Item.Type {
		case "reasoning", "command_execution", "file_change", "mcp_tool_call", "web_search", "todo_list", "error":
		default:
			log.Printf("codex events v1: ignored item type %q", OneLine(v.Item.Type, 80))
		}
	case "turn.completed":
		return []Event{{Kind: EventResult}}
	case "turn.failed":
		reason := v.Error.Message
		if reason == "" {
			reason = "Provider rejected the turn"
		}
		return []Event{{Kind: EventResult, IsError: true, Text: reason}}
	case "error":
		return []Event{{Kind: EventError, IsError: true, Text: v.Message}}
	case "turn.started", "item.started", "item.updated":
	default:
		log.Printf("codex events v1: ignored type %q", OneLine(v.Type, 80))
	}
	return nil
}
