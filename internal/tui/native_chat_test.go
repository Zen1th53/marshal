package tui

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/chattransport"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/chat"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/inbox"
)

// fakeChatTransport stands in for a provider's structured mode: no real
// provider session ever starts in these tests.
type fakeChatTransport struct {
	events chan chattransport.Event
	perms  chan chattransport.PermissionAsk

	mu       sync.Mutex
	session  chattransport.Session
	started  bool
	sent     []string
	decided  map[string]chattransport.Decision
	waiting  map[string]chan chattransport.Decision
	closed   bool
	resumeID string
}

func newFakeChatTransport() *fakeChatTransport {
	return &fakeChatTransport{
		events:  make(chan chattransport.Event, 64),
		perms:   make(chan chattransport.PermissionAsk, 8),
		decided: map[string]chattransport.Decision{},
		waiting: map[string]chan chattransport.Decision{},
	}
}

func (f *fakeChatTransport) Start(_ context.Context, s chattransport.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.session, f.started = s, true
	return nil
}
func (f *fakeChatTransport) Send(_ context.Context, t chattransport.Turn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, t.Text)
	return nil
}
func (f *fakeChatTransport) Events() <-chan chattransport.Event              { return f.events }
func (f *fakeChatTransport) Permissions() <-chan chattransport.PermissionAsk { return f.perms }
func (f *fakeChatTransport) Decide(_ context.Context, ask chattransport.PermissionAsk, d chattransport.Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decided[ask.ID] = d
	if ch := f.waiting[ask.ID]; ch != nil {
		ch <- d
		delete(f.waiting, ask.ID)
	}
	return nil
}
func (f *fakeChatTransport) Ask(ctx context.Context, ask chattransport.PermissionAsk) (chattransport.Decision, error) {
	f.mu.Lock()
	if d, ok := f.decided[ask.ID]; ok {
		f.mu.Unlock()
		return d, nil
	}
	ch, ok := f.waiting[ask.ID]
	if !ok {
		ch = make(chan chattransport.Decision, 1)
		f.waiting[ask.ID] = ch
		f.mu.Unlock()
		if ask.Provider == "" {
			ask.Provider = "claude"
		}
		if ask.Summary == "" {
			ask.Summary = chattransport.Summarize(ask.Input, 160)
		}
		f.perms <- ask
	} else {
		f.mu.Unlock()
	}
	select {
	case d := <-ch:
		return d, nil
	case <-ctx.Done():
		return chattransport.Decision{}, ctx.Err()
	}
}
func (f *fakeChatTransport) Resume(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumeID = id
	return nil
}
func (f *fakeChatTransport) SessionID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.session.SessionID
}
func (f *fakeChatTransport) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}
func (f *fakeChatTransport) decision(id string) (chattransport.Decision, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.decided[id]
	return d, ok
}
func (f *fakeChatTransport) sentTurns() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

// waitFor polls cond for up to five seconds.
func chatWaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func boardWithNativeChat(t *testing.T) (*Workspace, *app.Runtime, *fakeChatTransport) {
	t.Helper()
	w, runtime := realControlWorkspace(t, "SESSION-native-chat", false)
	w.screen = NewScreen(nil)
	w.mu.Lock()
	w.state.Marshal = &MarshalPanel{RunID: "run"}
	w.mu.Unlock()
	tr := newFakeChatTransport()
	w.attachNativeChat(tr, runtime.ProjectRoot(), nil, nil, nil, nil, "", false)
	t.Cleanup(w.stopNativeChat)
	return w, runtime, tr
}

// A provider permission asked in the native chat reaches the Board inbox,
// never a popup; the inbox decision is recorded as PERMISSION_DECIDED like
// every inbox decision and goes back to the provider.
func TestNativeChatPermissionGoesToInboxNotPopup(t *testing.T) {
	w, runtime, tr := boardWithNativeChat(t)
	verbs := popupRecorder(t, w, "A")
	tr.perms <- chattransport.PermissionAsk{ID: "toolu_1", Provider: "claude", ToolName: "Bash", Summary: "npm view deja-vu"}
	chatWaitFor(t, "the ask in the inbox", func() bool { return len(w.boardInbox()) == 1 })
	item := w.boardInbox()[0]
	if item.Request.Kind != app.MarshalToolPermissionKind || item.Request.Object != "Bash npm view deja-vu" || item.Gate != "single" {
		t.Fatalf("inbox item: %+v", item)
	}
	// The permission queue never sees it: no popup can be chosen for it,
	// and running the queue (as a busy Board does) opens none.
	if !w.permissions.queue.Empty() {
		t.Fatal("the ask went through the popup queue")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	w.runPermissionQueue(ctx)
	cancel()
	// /view classic hands queued requests to popups; this one stays.
	w.setBoardView(true)
	if len(w.boardInbox()) != 1 || !w.permissions.queue.Empty() {
		t.Fatal("/view classic moved the chat ask out of the inbox")
	}
	w.setBoardView(false)
	if popups := popupOpened(verbs()); len(popups) != 0 {
		t.Fatalf("a popup opened for a native chat ask: %v", verbs())
	}
	w.inboxView.Update(w.boardInbox())
	if !w.inboxView.Begin('a') {
		t.Fatal("inbox did not take a")
	}
	d, ok := w.inboxView.Confirm()
	if !ok {
		t.Fatal("single gate did not confirm")
	}
	if err := w.decideInbox(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	got, ok := tr.decision("toolu_1")
	if !ok || !got.Allow {
		t.Fatalf("decision not delivered to the provider: %+v %v", got, ok)
	}
	if len(w.boardInbox()) != 0 {
		t.Fatal("decided ask is still in the inbox")
	}
	events, err := runtime.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["kind"] == app.MarshalToolPermissionKind {
			found = true
			for _, k := range []string{"object", "scope", "requester", "reason", "allow", "actor", "source", "approval_binding"} {
				if _, ok := e.Data[k]; !ok {
					t.Fatalf("PERMISSION_DECIDED lacks %q: %v", k, e.Data)
				}
			}
			if e.Data["allow"] != true || !strings.HasPrefix(e.Data["source"].(string), "operator inbox;") || e.Data["approval_binding"] != "toolu_1" {
				t.Fatalf("audit: %v", e.Data)
			}
		}
	}
	if !found {
		t.Fatal("no PERMISSION_DECIDED for the chat ask")
	}
}

func TestNativeChatDenyCarriesReason(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	tr.perms <- chattransport.PermissionAsk{ID: "toolu_2", Provider: "claude", ToolName: "Write", Summary: "/etc/passwd"}
	chatWaitFor(t, "the ask in the inbox", func() bool { return len(w.boardInbox()) == 1 })
	w.inboxView.Update(w.boardInbox())
	w.inboxView.Begin('r')
	w.inboxView.Input("outside the worktree")
	d, ok := w.inboxView.Confirm()
	if !ok {
		t.Fatal("return did not confirm")
	}
	if err := w.decideInbox(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	got, _ := tr.decision("toolu_2")
	if got.Allow || !strings.Contains(got.Message, "outside the worktree") {
		t.Fatalf("deny: %+v", got)
	}
}

func TestNativeChatStopWithdrawsItsAsks(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	tr.perms <- chattransport.PermissionAsk{ID: "toolu_3", Provider: "claude", ToolName: "Bash"}
	chatWaitFor(t, "the ask in the inbox", func() bool { return len(w.boardInbox()) == 1 })
	w.stopNativeChat()
	if len(w.boardInbox()) != 0 || !tr.closed {
		t.Fatal("stopping the chat left its ask in the inbox or the transport open")
	}
}

// An idle chat costs no frames: repaints follow events, never a clock.
func TestNativeChatIdleIsZeroRedraws(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	var repaints atomic.Int64
	w.repaint = make(chan struct{}, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-w.repaint:
				repaints.Add(1)
			case <-stop:
				return
			}
		}
	}()
	defer func() { close(stop); <-done }()
	tr.events <- chattransport.Event{Kind: chattransport.EventTextDelta, MessageID: "m", Text: "hello"}
	tr.events <- chattransport.Event{Kind: chattransport.EventResult}
	chatWaitFor(t, "a repaint for the stream", func() bool { return repaints.Load() > 0 })
	w.frameLines(160, 40) // the first paint installs the header
	w.chat.mu.Lock()
	v := w.chat.view.Version()
	w.chat.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	before := repaints.Load()
	for range 10 {
		w.frameLines(160, 40)
	}
	time.Sleep(300 * time.Millisecond)
	if after := repaints.Load(); after != before {
		t.Fatalf("idle chat asked for %d repaints", after-before)
	}
	w.chat.mu.Lock()
	moved := w.chat.view.Version() != v
	w.chat.mu.Unlock()
	if moved {
		t.Fatal("painting an idle chat changed it")
	}
	// An event that changes nothing visible repaints nothing either.
	tr.events <- chattransport.Event{Kind: chattransport.EventToolResult, ToolID: "unknown"}
	time.Sleep(100 * time.Millisecond)
	if after := repaints.Load(); after != before {
		t.Fatalf("a no-op event asked for %d repaints", after-before)
	}
}

func TestNativeChatDocksLeftOfBoard(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	tr.events <- chattransport.Event{Kind: chattransport.EventSession, SessionID: "sess-1", Model: "claude-opus"}
	tr.events <- chattransport.Event{Kind: chattransport.EventText, MessageID: "m1", Text: "T-023 waits for the inbox."}
	chatWaitFor(t, "the reply", func() bool {
		w.chat.mu.Lock()
		defer w.chat.mu.Unlock()
		return len(w.chat.view.Entries()) > 0
	})
	if w.boardLayoutMode() != layout.Split {
		t.Fatal("the Board does not paint its split form beside the native chat")
	}
	cols, rows := 170, 40
	lines, _, _ := w.frameLines(cols, rows)
	chatW, _, _ := layout.SplitWidths(cols)
	if len(lines) != rows {
		t.Fatalf("%d rows", len(lines))
	}
	plain := func(s string) string { return StripANSI(s) }
	if !strings.HasPrefix(plain(lines[0]), "MARSHAL  claude") {
		t.Fatalf("chat header: %q", plain(lines[0]))
	}
	joined := ""
	for _, l := range lines {
		p := []rune(plain(l))
		if VisibleLen(l) != cols {
			t.Fatalf("row width %d: %q", VisibleLen(l), plain(l))
		}
		joined += string(p[:chatW]) + "\n"
		if r := p[chatW]; r != '│' && r != '|' {
			t.Fatalf("no rule at column %d: %q", chatW+1, string(p))
		}
	}
	if !strings.Contains(joined, "T-023 waits for the inbox.") {
		t.Fatalf("reply not in the chat half:\n%s", joined)
	}
	// F11 focuses the composer; typing goes to the chat, Enter sends a turn.
	w.handleTerminalKey(t.Context(), KeyEvent{Type: KeyF11})
	if !w.nativeChatFocused() {
		t.Fatal("F11 did not focus the native chat")
	}
	for _, r := range "explain T-031" {
		w.handleTerminalKey(t.Context(), KeyEvent{Type: KeyRune, Rune: r})
	}
	if w.composer.Text() != "" {
		t.Fatal("typing leaked into the Board composer")
	}
	w.handleTerminalKey(t.Context(), KeyEvent{Type: KeyEnter})
	chatWaitFor(t, "the turn", func() bool { return len(tr.sentTurns()) == 1 })
	if tr.sentTurns()[0] != "explain T-031" {
		t.Fatalf("sent %q", tr.sentTurns())
	}
	w.handleTerminalKey(t.Context(), KeyEvent{Type: KeyEsc})
	if w.nativeChatFocused() {
		t.Fatal("Esc did not return to the Board")
	}
	// /view classic gives the whole screen back to the classic frame.
	w.setBoardView(true)
	if w.nativeChatShown() || w.boardLayoutMode() == layout.Split {
		t.Fatal("the chat stayed docked in /view classic")
	}
	w.setBoardView(false)
}

func TestViewChatSettingPersists(t *testing.T) {
	root := t.TempDir()
	if loadChatView(root) != "tmux" {
		t.Fatal("default chat view is not tmux")
	}
	if err := saveChatView(root, "native"); err != nil {
		t.Fatal(err)
	}
	if loadChatView(root) != "native" {
		t.Fatal("native not persisted")
	}
	if err := saveChatView(root, "tmux"); err != nil {
		t.Fatal(err)
	}
	if loadChatView(root) != "tmux" {
		t.Fatal("tmux not persisted")
	}
}

func TestViewChatTmuxStopsNativeChat(t *testing.T) {
	w, runtime, tr := boardWithNativeChat(t)
	out, err := w.handleViewCommand(t.Context(), []string{"chat"})
	if err != nil || !strings.Contains(out, "native widget") {
		t.Fatalf("status: %q %v", out, err)
	}
	out, err = w.handleViewCommand(t.Context(), []string{"chat", "tmux"})
	if err != nil || !strings.HasPrefix(out, "Chat view: tmux") {
		t.Fatalf("tmux: %q %v", out, err)
	}
	if w.nativeChatActive() || !tr.closed || loadChatView(runtime.ProjectRoot()) != "tmux" {
		t.Fatal("/view chat tmux did not stop the native chat and save the setting")
	}
}

func TestChatProcessEnvHidesTmuxClient(t *testing.T) {
	// Only the environment builder runs here; no tmux command is issued.
	t.Setenv("TMUX", "/nonexistent/should-not-leak,1,0")
	t.Setenv("TMUX_PANE", "%99")
	env := chatProcessEnv([]string{"MARSHAL_X=1"})
	for _, e := range env {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") {
			t.Fatalf("tmux client variable reached the provider: %q", e)
		}
	}
	if env[len(env)-1] != "MARSHAL_X=1" {
		t.Fatal("briefing variable lost")
	}
}

func TestStripMarshalKickoff(t *testing.T) {
	root := t.TempDir()
	k := marshalKickoffArgs("claude", root)[0]
	args, opening := stripMarshalKickoff("claude", root, []string{"--append-system-prompt", "x", "--mcp-config", "/c.json", "--", k})
	if opening != k || strings.Join(args, " ") != "--append-system-prompt x --mcp-config /c.json" {
		t.Fatalf("args %q opening %q", args, opening)
	}
	args, opening = stripMarshalKickoff("claude", root, []string{"--model", "m"})
	if opening != "" || len(args) != 2 {
		t.Fatal("args without a kickoff changed")
	}
}

func TestLoadNativeChatHistoryRestoresTranscript(t *testing.T) {
	root := t.TempDir()
	hist := t.TempDir()
	old := nativeChatHistoryDir
	nativeChatHistoryDir = func(string) (string, error) { return hist, nil }
	t.Cleanup(func() { nativeChatHistoryDir = old })
	lines := `{"type":"user","sessionId":"sess-9","cwd":"` + root + `","timestamp":"2026-11-06T10:00:00Z","message":{"role":"user","content":"Why is T-023 parked?"}}
{"type":"assistant","sessionId":"sess-9","cwd":"` + root + `","timestamp":"2026-11-06T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"It waits for the inbox."}]}}
`
	if err := os.WriteFile(hist+"/sess-9.jsonl", []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadNativeChatHistory(root, "sess-9", 200)
	if len(got) != 2 || got[0].Role != chat.You || got[1].Role != chat.Marshal || got[1].Text != "It waits for the inbox." {
		t.Fatalf("history: %+v", got)
	}
	if loadNativeChatHistory(root, "../escape", 200) != nil {
		t.Fatal("a non-native session id was read")
	}
}

var (
	_ = permission.Request{}
	_ = inbox.Decision{}
)

func decideInboxAs(t *testing.T, w *Workspace, action rune) {
	t.Helper()
	w.inboxView.Update(w.boardInbox())
	if !w.inboxView.Begin(action) {
		t.Fatalf("inbox did not take %c", action)
	}
	d, ok := w.inboxView.Confirm()
	if !ok {
		t.Fatalf("%c did not confirm", action)
	}
	if err := w.decideInbox(t.Context(), d); err != nil {
		t.Fatal(err)
	}
}

func setMarshalRun(w *Workspace, runID string) {
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = runID
	m.mu.Unlock()
}

// A (allow for run) grants the tool for the open run only: the next ask for
// it in that run is decided by the grant through the canonical path, other
// tools and other runs still come to the inbox.
func TestNativeChatAllowForRunIsScopedToTheRun(t *testing.T) {
	w, runtime, tr := boardWithNativeChat(t)
	setMarshalRun(w, "RUN-A")
	tr.perms <- chattransport.PermissionAsk{ID: "toolu_a1", Provider: "claude", ToolName: "Bash", Summary: "go test ./..."}
	chatWaitFor(t, "the first ask", func() bool { return len(w.boardInbox()) == 1 })
	decideInboxAs(t, w, 'A')
	if d, ok := tr.decision("toolu_a1"); !ok || !d.Allow {
		t.Fatal("A did not allow the asked call")
	}
	tr.perms <- chattransport.PermissionAsk{ID: "toolu_a2", Provider: "claude", ToolName: "Bash", Summary: "go vet ./..."}
	chatWaitFor(t, "the run grant to decide", func() bool { _, ok := tr.decision("toolu_a2"); return ok })
	if d, _ := tr.decision("toolu_a2"); !d.Allow || len(w.boardInbox()) != 0 {
		t.Fatalf("same tool, same run was not allowed by the grant: %+v inbox=%d", d, len(w.boardInbox()))
	}
	events, err := runtime.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	granted := false
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["approval_binding"] == "toolu_a2" {
			src, _ := e.Data["source"].(string)
			granted = e.Data["allow"] == true && strings.HasPrefix(src, runGrantSource) && strings.Contains(src, "run=RUN-A") && e.Data["run_id"] == "RUN-A"
		}
	}
	if !granted {
		t.Fatal("the run-grant decision was not audited as PERMISSION_DECIDED")
	}
	// Another tool in the same run still asks.
	tr.perms <- chattransport.PermissionAsk{ID: "toolu_a3", Provider: "claude", ToolName: "Write", Summary: "x.go"}
	chatWaitFor(t, "the other tool in the inbox", func() bool { return len(w.boardInbox()) == 1 })
	decideInboxAs(t, w, 'd')
	// The same tool in another run asks again.
	setMarshalRun(w, "RUN-B")
	tr.perms <- chattransport.PermissionAsk{ID: "toolu_b1", Provider: "claude", ToolName: "Bash", Summary: "go test ./..."}
	chatWaitFor(t, "the next run's ask in the inbox", func() bool { return len(w.boardInbox()) == 1 })
	if _, ok := tr.decision("toolu_b1"); ok {
		t.Fatal("a grant for RUN-A decided an ask in RUN-B")
	}
}

// Allow once (a) grants nothing beyond the call.
func TestNativeChatAllowOnceGrantsNothingMore(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	setMarshalRun(w, "RUN-A")
	tr.perms <- chattransport.PermissionAsk{ID: "toolu_o1", Provider: "claude", ToolName: "Bash"}
	chatWaitFor(t, "the ask", func() bool { return len(w.boardInbox()) == 1 })
	decideInboxAs(t, w, 'a')
	tr.perms <- chattransport.PermissionAsk{ID: "toolu_o2", Provider: "claude", ToolName: "Bash"}
	chatWaitFor(t, "the second ask in the inbox", func() bool { return len(w.boardInbox()) == 1 })
}

// /harness verify records the native chat transport contract.
func TestHarnessVerifyRecordsChatTransportRange(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-chat-transport-range", false)
	cases := []struct{ provider, version, result string }{
		{"claude", "2.1.318 (Claude Code)", "qualified"},
		{"claude", "1.0.99 (Claude Code)", "refused"},
		{"codex", "0.162.1", "unsupported"},
	}
	for _, c := range cases {
		line, err := w.recordChatTransportQualification(t.Context(), c.provider, c.version, "EV-1")
		if err != nil || !strings.Contains(line, "Native chat transport") {
			t.Fatalf("%v: %q %v", c, line, err)
		}
	}
	events, err := runtime.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range events {
		if e.Type != chatTransportQualifiedEvent {
			continue
		}
		got = append(got, e.Data["provider"].(string)+" "+e.Data["result"].(string))
		if e.Data["provider"] == "claude" && (e.Data["range_min"] != chattransport.ClaudeRange.Min || e.Data["range_below"] != chattransport.ClaudeRange.Below) {
			t.Fatalf("range not recorded: %v", e.Data)
		}
	}
	if strings.Join(got, ",") != "claude qualified,claude refused,codex unsupported" {
		t.Fatalf("recorded %v", got)
	}
}
