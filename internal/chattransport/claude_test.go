package chattransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProc is a provider process whose stdout the test writes and whose
// stdin the test reads.
type fakeProc struct {
	args   []string
	in     chan string
	outR   *io.PipeReader
	outW   *io.PipeWriter
	killed chan struct{}
	once   sync.Once
}

func newFakeProc(args []string) *fakeProc {
	p := &fakeProc{args: args, killed: make(chan struct{}), in: make(chan string, 16)}
	p.outR, p.outW = io.Pipe()
	return p
}

type fakeStdin struct{ p *fakeProc }

func (s fakeStdin) Write(b []byte) (int, error) {
	select {
	case <-s.p.killed:
		return 0, io.ErrClosedPipe
	default:
	}
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		s.p.in <- l
	}
	return len(b), nil
}
func (s fakeStdin) Close() error { return nil }

func (p *fakeProc) Stdin() io.WriteCloser { return fakeStdin{p} }
func (p *fakeProc) Stdout() io.Reader     { return p.outR }
func (p *fakeProc) Wait() error           { <-p.killed; return nil }
func (p *fakeProc) Kill() error {
	p.once.Do(func() { close(p.killed); _ = p.outW.Close() })
	return nil
}

// exit ends the process the way a provider exit does: stdout closes.
func (p *fakeProc) exit() { p.Kill() }

func (p *fakeProc) emit(t *testing.T, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if _, err := io.WriteString(p.outW, l+"\n"); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}
}

func (p *fakeProc) readTurn(t *testing.T) string {
	t.Helper()
	select {
	case l := <-p.in:
		return l
	case <-time.After(5 * time.Second):
		t.Fatal("no turn on stdin")
	}
	return ""
}

type fakeRunner struct {
	mu      sync.Mutex
	version string
	procs   []*fakeProc
	started chan *fakeProc
}

func newFakeRunner(version string) *fakeRunner {
	return &fakeRunner{version: version, started: make(chan *fakeProc, 8)}
}

func (r *fakeRunner) Start(_ context.Context, _, _ string, args, _ []string) (Process, error) {
	p := newFakeProc(slices.Clone(args))
	r.mu.Lock()
	r.procs = append(r.procs, p)
	r.mu.Unlock()
	r.started <- p
	return p, nil
}

func (r *fakeRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return []byte(r.version), nil
}

func (r *fakeRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.procs)
}

func next(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	return Event{}
}

func startClaude(t *testing.T, sessionID string) (*Claude, *fakeRunner, *fakeProc) {
	t.Helper()
	r := newFakeRunner("2.1.318 (Claude Code)\n")
	c := NewClaude(r)
	t.Cleanup(func() { c.Close() })
	if err := c.Start(context.Background(), Session{Binary: "claude", Dir: "/p", Args: []string{"--mcp-config", "/b/claude_mcp.json"}, SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	return c, r, <-r.started
}

func TestClaudeArgsAreStructuredFlagsThenLaunchArgs(t *testing.T) {
	got := ClaudeArgs(Session{Args: []string{"--allowedTools", "mcp__marshal", "--mcp-config", "/x.json"}}, "S-1")
	want := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--permission-prompt-tool", ClaudePermissionTool, "--resume", "S-1", "--allowedTools", "mcp__marshal", "--mcp-config", "/x.json"}
	if !slices.Equal(got, want) {
		t.Fatalf("args\n got %q\nwant %q", got, want)
	}
	if slices.Contains(ClaudeArgs(Session{}, ""), "--resume") {
		t.Fatal("a fresh session must not resume")
	}
}

func TestClaudeRefusesUnqualifiedVersionBeforeAnyProcess(t *testing.T) {
	for _, v := range []string{"1.0.99 (Claude Code)", "3.0.0 (Claude Code)", "garbage"} {
		r := newFakeRunner(v)
		c := NewClaude(r)
		err := c.Start(context.Background(), Session{Binary: "claude"})
		if !errors.Is(err, ErrUnqualified) {
			t.Fatalf("%q: want ErrUnqualified, got %v", v, err)
		}
		if r.count() != 0 {
			t.Fatalf("%q: a process started for an unqualified version", v)
		}
		if err := c.Send(context.Background(), Turn{Text: "hi"}); err == nil {
			t.Fatalf("%q: a turn was accepted by a refused transport", v)
		}
	}
}

func TestNewRefusesProvidersWithoutAQualifiedMode(t *testing.T) {
	for _, p := range []string{"codex", "opencode", "agy", "antigravity"} {
		if _, err := New(p, newFakeRunner("")); !errors.Is(err, ErrUnqualified) {
			t.Fatalf("%s: want ErrUnqualified, got %v", p, err)
		}
	}
	if tr, err := New("claude", newFakeRunner("")); err != nil || tr == nil {
		t.Fatalf("claude: %v", err)
	}
}

func TestClaudeStreamsATurn(t *testing.T) {
	c, _, p := startClaude(t, "")
	if err := c.Send(context.Background(), Turn{Text: "why is T-023 parked?"}); err != nil {
		t.Fatal(err)
	}
	var turn struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct{ Type, Text string }
		}
	}
	if err := json.Unmarshal([]byte(p.readTurn(t)), &turn); err != nil {
		t.Fatal(err)
	}
	if turn.Type != "user" || turn.Message.Role != "user" || len(turn.Message.Content) != 1 || turn.Message.Content[0].Text != "why is T-023 parked?" {
		t.Fatalf("turn line: %+v", turn)
	}
	p.emit(t,
		`not json at all`,
		`{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-opus","claude_code_version":"2.1.318"}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"T-023 "}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"waits."}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{"}}}`,
		`{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"T-023 waits."}]}}`,
		`{"type":"assistant","message":{"id":"msg_1","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"go test ./internal/app/...\n","description":"run tests"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu_1","content":[{"type":"text","text":"ok\n  pkg 0.3s"}],"is_error":false}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"T-023 waits.","session_id":"sess-1","total_cost_usd":0.01}`,
	)
	want := []Event{
		{Kind: EventSession, SessionID: "sess-1", Model: "claude-opus"},
		{Kind: EventTextDelta, MessageID: "msg_1", Text: "T-023 "},
		{Kind: EventTextDelta, MessageID: "msg_1", Text: "waits."},
		{Kind: EventText, MessageID: "msg_1", Text: "T-023 waits."},
		{Kind: EventToolUse, MessageID: "msg_1", ToolID: "tu_1", ToolName: "Bash", ToolInput: "go test ./internal/app/..."},
		{Kind: EventToolResult, ToolID: "tu_1", Text: "ok pkg 0.3s"},
	}
	for i, w := range want {
		if got := next(t, c.Events()); got != w {
			t.Fatalf("event %d\n got %+v\nwant %+v", i, got, w)
		}
	}
	if got := next(t, c.Events()); got.Kind != EventResult || got.IsError || got.CostUSD == nil {
		t.Fatalf("result: %+v", got)
	}
	if c.SessionID() != "sess-1" {
		t.Fatalf("session id %q", c.SessionID())
	}
}

func TestClaudePermissionAskWaitsForDecide(t *testing.T) {
	c, _, _ := startClaude(t, "")
	input := json.RawMessage(`{"command":"npm view deja-vu"}`)
	type answer struct {
		d   Decision
		err error
	}
	done := make(chan answer, 1)
	go func() {
		d, err := c.Ask(context.Background(), PermissionAsk{ID: "tu_9", ToolName: "Bash", Input: input})
		done <- answer{d, err}
	}()
	var ask PermissionAsk
	select {
	case ask = <-c.Permissions():
	case <-time.After(5 * time.Second):
		t.Fatal("ask never surfaced")
	}
	if ask.ID != "tu_9" || ask.ToolName != "Bash" || ask.Summary != "npm view deja-vu" || ask.Provider != "claude" {
		t.Fatalf("ask: %+v", ask)
	}
	select {
	case a := <-done:
		t.Fatalf("Ask returned before a decision: %+v", a)
	case <-time.After(50 * time.Millisecond):
	}
	if err := c.Decide(context.Background(), ask, Decision{Allow: true}); err != nil {
		t.Fatal(err)
	}
	a := <-done
	if a.err != nil || !a.d.Allow {
		t.Fatalf("answer: %+v", a)
	}
	if err := c.Decide(context.Background(), ask, Decision{Allow: true}); err == nil {
		t.Fatal("a decided ask was decided twice")
	}
	var res map[string]any
	_ = json.Unmarshal(ClaudePermissionResult(input, a.d), &res)
	if res["behavior"] != "allow" || res["updatedInput"].(map[string]any)["command"] != "npm view deja-vu" {
		t.Fatalf("allow result %v", res)
	}
	_ = json.Unmarshal(ClaudePermissionResult(input, Decision{}), &res)
	if res["behavior"] != "deny" || !strings.Contains(res["message"].(string), "inbox") {
		t.Fatalf("deny result %v", res)
	}
}

func TestClaudeLongPolledAskKeepsItsDecision(t *testing.T) {
	c, _, _ := startClaude(t, "")
	ask := PermissionAsk{ID: "tu_2", ToolName: "Write"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.Ask(ctx, ask); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first poll: %v", err)
	}
	<-c.Permissions()
	// Decided between polls: the next poll gets it and the ask is not
	// surfaced a second time.
	if err := c.Decide(context.Background(), ask, Decision{Allow: false, Message: "no"}); err != nil {
		t.Fatal(err)
	}
	d, err := c.Ask(context.Background(), ask)
	if err != nil || d.Allow || d.Message != "no" {
		t.Fatalf("second poll: %+v %v", d, err)
	}
	select {
	case again := <-c.Permissions():
		t.Fatalf("ask surfaced twice: %+v", again)
	default:
	}
}

func TestClaudeResumesTheSameSessionAfterExit(t *testing.T) {
	c, r, p := startClaude(t, "")
	if err := c.Send(context.Background(), Turn{Text: "one"}); err != nil {
		t.Fatal(err)
	}
	p.readTurn(t)
	p.emit(t, `{"type":"system","subtype":"init","session_id":"sess-7"}`)
	next(t, c.Events())
	p.exit()
	if e := next(t, c.Events()); e.Kind != EventExit {
		t.Fatalf("want exit, got %+v", e)
	}
	if err := c.Send(context.Background(), Turn{Text: "two"}); err != nil {
		t.Fatal(err)
	}
	p2 := <-r.started
	i := slices.Index(p2.args, "--resume")
	if i < 0 || p2.args[i+1] != "sess-7" {
		t.Fatalf("second process does not resume sess-7: %q", p2.args)
	}
	if !strings.Contains(p2.readTurn(t), `"two"`) {
		t.Fatal("turn not delivered to the resumed process")
	}
	// Explicit Resume replaces the process and binds the given session.
	if err := c.Resume(context.Background(), "sess-8"); err != nil {
		t.Fatal(err)
	}
	p3 := <-r.started
	if i := slices.Index(p3.args, "--resume"); i < 0 || p3.args[i+1] != "sess-8" || c.SessionID() != "sess-8" {
		t.Fatalf("resume args %q id %q", p3.args, c.SessionID())
	}
}

func TestClaudeStartResumesSavedSession(t *testing.T) {
	_, _, p := startClaude(t, "sess-saved")
	if i := slices.Index(p.args, "--resume"); i < 0 || p.args[i+1] != "sess-saved" {
		t.Fatalf("saved session not resumed: %q", p.args)
	}
	if p.args[len(p.args)-1] != "/b/claude_mcp.json" {
		t.Fatalf("launch args not kept last: %q", p.args)
	}
}

func TestClaudeCloseDeniesPendingAsks(t *testing.T) {
	c, _, _ := startClaude(t, "")
	done := make(chan Decision, 1)
	go func() {
		d, _ := c.Ask(context.Background(), PermissionAsk{ID: "tu_x", ToolName: "Bash"})
		done <- d
	}()
	<-c.Permissions()
	c.Close()
	select {
	case d := <-done:
		if d.Allow {
			t.Fatal("close allowed a pending ask")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending ask never answered on close")
	}
}

func TestOneLineAndSummarize(t *testing.T) {
	if got := OneLine("a\x1b[31m\n\tb", 0); got != "a [31m b" {
		t.Fatalf("OneLine control: %q", got)
	}
	if got := OneLine("abcdef", 4); got != "abc…" {
		t.Fatalf("OneLine bound: %q", got)
	}
	if got := Summarize(json.RawMessage(`{"file_path":"/a/b.go","content":"x"}`), 40); got != "/a/b.go" {
		t.Fatalf("Summarize: %q", got)
	}
}
