//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/chattransport"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

func nativeChatLaunchFixture(t *testing.T) (*Workspace, string, *briefingDir, *fakeChatTransport) {
	t.Helper()
	w, runtime := realControlWorkspace(t, "SESSION-native-launch", false)
	w.screen = NewScreen(nil)
	root := runtime.ProjectRoot()
	dir, err := newBriefingDir(root, "claude")
	if err != nil {
		t.Fatal(err)
	}
	cfg := `{"mcpServers":{"marshal":{"command":"marshal","args":["mcp","serve","--stdio"]}}}`
	if err := os.WriteFile(filepath.Join(dir.path, "claude_mcp.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := newFakeChatTransport()
	oldProbe, oldTr, oldHist := nativeChatProbe, nativeChatTransport, nativeChatHistoryDir
	nativeChatProbe = func(context.Context, string, string) error { return nil }
	nativeChatTransport = func(string) (chattransport.Transport, error) { return tr, nil }
	hist := t.TempDir()
	nativeChatHistoryDir = func(string) (string, error) { return hist, nil }
	t.Cleanup(func() { nativeChatProbe, nativeChatTransport, nativeChatHistoryDir = oldProbe, oldTr, oldHist })
	line := `{"type":"assistant","sessionId":"sess-old","cwd":"` + root + `","timestamp":"2026-11-06T09:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Earlier answer."}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(hist, "sess-old.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.stopNativeChat)
	return w, root, dir, tr
}

func mcpServerEnv(t *testing.T, dir *briefingDir) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir.path, "claude_mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Env map[string]any `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.MCPServers["marshal"].Env
}

// The native chat resumes the session the chat binding holds (shared with
// the tmux chat), shows that session's transcript from the native history,
// and records the provider's new session id for the next launch.
func TestNativeChatLaunchResumesBoundSession(t *testing.T) {
	w, root, dir, tr := nativeChatLaunchFixture(t)
	if err := saveChatBinding(root, chatBinding{Provider: "claude", SessionID: "sess-old", RunID: "RUN-1"}); err != nil {
		t.Fatal(err)
	}
	kick := marshalKickoffArgs("claude", root)[0]
	out, err := w.launchNativeChat(t.Context(), "claude", root, "/opt/claude", []string{"--mcp-config", filepath.Join(dir.path, "claude_mcp.json"), "--", kick}, []string{"MARSHAL_X=1"}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "native") || !w.nativeChatActive() {
		t.Fatalf("launch: %q", out)
	}
	s := tr.session
	if s.SessionID != "sess-old" || s.Binary != "/opt/claude" || s.Dir != root {
		t.Fatalf("session: %+v", s)
	}
	if slices.Contains(s.Args, kick) || slices.Contains(s.Args, "--") {
		t.Fatalf("kickoff left in argv: %q", s.Args)
	}
	if slices.ContainsFunc(s.Env, func(e string) bool { return strings.HasPrefix(e, "TMUX=") }) || !slices.Contains(s.Env, "MARSHAL_X=1") {
		t.Fatal("provider environment carries the tmux client or lost the briefing")
	}
	if len(tr.sentTurns()) != 0 {
		t.Fatal("a resumed session was sent the opening turn again")
	}
	w.chat.mu.Lock()
	entries := w.chat.view.Entries()
	w.chat.mu.Unlock()
	if len(entries) < 2 || entries[0].Text != "Earlier answer." {
		t.Fatalf("transcript not restored: %+v", entries)
	}
	path, _ := mcpServerEnv(t, dir)["MARSHAL_CHAT_PERMISSION_CONFIG"].(string)
	if path == "" {
		t.Fatal("permission broker not bound in the MCP config")
	}
	tr.events <- chattransport.Event{Kind: chattransport.EventSession, SessionID: "sess-new"}
	chatWaitFor(t, "the new session in the binding", func() bool { return loadChatBinding(root).SessionID == "sess-new" })
	if b := loadChatBinding(root); b.RunID != "RUN-1" || b.Provider != "claude" {
		t.Fatalf("binding: %+v", b)
	}
	w.stopNativeChat()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broker config outlived the chat: %v", err)
	}
}

func TestNativeChatFreshSessionSendsOpening(t *testing.T) {
	w, root, dir, tr := nativeChatLaunchFixture(t)
	kick := marshalKickoffArgs("claude", root)[0]
	if _, err := w.launchNativeChat(t.Context(), "claude", root, "/opt/claude", []string{"--", kick}, nil, dir, nil); err != nil {
		t.Fatal(err)
	}
	if tr.session.SessionID != "" {
		t.Fatal("a fresh chat resumed something")
	}
	chatWaitFor(t, "the opening turn", func() bool { return len(tr.sentTurns()) == 1 })
	if tr.sentTurns()[0] != kick {
		t.Fatalf("opening %q", tr.sentTurns())
	}
}

// Outside the qualified range nothing changes and the caller keeps tmux.
func TestNativeChatUnqualifiedChangesNothing(t *testing.T) {
	w, root, dir, tr := nativeChatLaunchFixture(t)
	nativeChatProbe = func(context.Context, string, string) error {
		return chattransport.ErrUnqualified
	}
	before, _ := os.ReadFile(filepath.Join(dir.path, "claude_mcp.json"))
	_, err := w.launchNativeChat(t.Context(), "claude", root, "/opt/claude", nil, nil, dir, nil)
	if !errors.Is(err, chattransport.ErrUnqualified) {
		t.Fatalf("want ErrUnqualified, got %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir.path, "claude_mcp.json"))
	if string(before) != string(after) || w.nativeChatActive() || tr.started {
		t.Fatal("an unqualified launch changed the MCP config or started a transport")
	}
	if _, err := w.launchNativeChat(t.Context(), "codex", root, "/opt/codex", nil, nil, dir, nil); !errors.Is(err, chattransport.ErrUnqualified) {
		t.Fatalf("codex: want ErrUnqualified, got %v", err)
	}
}

// The MCP permission tool's broker: a pending ask long-polls, lands in the
// inbox, and the inbox decision is the tool's answer.
func TestNativeChatPermissionBrokerRoundTrip(t *testing.T) {
	w, root, dir, tr := nativeChatLaunchFixture(t)
	if _, err := w.launchNativeChat(t.Context(), "claude", root, "/opt/claude", nil, nil, dir, nil); err != nil {
		t.Fatal(err)
	}
	old := chatPermissionPoll
	chatPermissionPoll = 100 * time.Millisecond
	t.Cleanup(func() { chatPermissionPoll = old })
	path, _ := mcpServerEnv(t, dir)["MARSHAL_CHAT_PERMISSION_CONFIG"].(string)
	cfg, err := proposalmcp.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"tool_name":"Bash","input":{"command":"npm view deja-vu"},"tool_use_id":"toolu_42"}`)
	first, err := proposalmcp.Call(t.Context(), cfg, "permission_prompt", raw)
	if err != nil || string(first) != `{"pending":true}` {
		t.Fatalf("first poll: %s %v", first, err)
	}
	chatWaitFor(t, "the ask in the inbox", func() bool { return len(w.boardInbox()) == 1 })
	if obj := w.boardInbox()[0].Request.Object; obj != "Bash npm view deja-vu" {
		t.Fatalf("inbox object %q", obj)
	}
	w.inboxView.Update(w.boardInbox())
	w.inboxView.Begin('a')
	d, _ := w.inboxView.Confirm()
	if err := w.decideInbox(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	second, err := proposalmcp.Call(t.Context(), cfg, "permission_prompt", raw)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Behavior     string         `json:"behavior"`
		UpdatedInput map[string]any `json:"updatedInput"`
	}
	if json.Unmarshal(second, &res) != nil || res.Behavior != "allow" || res.UpdatedInput["command"] != "npm view deja-vu" {
		t.Fatalf("answer: %s", second)
	}
	if _, err := proposalmcp.Call(t.Context(), cfg, "submit", raw); err == nil {
		t.Fatal("the chat broker accepted another method")
	}
	_ = tr
}

// A runtime decision request goes to the native chat as a message turn
// (never a tmux pane), and the Marshal's answer lets the work proceed.
func TestNativeChatReceivesMarshalDecisionRequests(t *testing.T) {
	marshalDecidesReads(t)
	w, rt, tr := boardWithNativeChat(t)
	req := permission.Request{Kind: "read", Object: t.TempDir(), Scope: "one run", Who: "worker-1", Reason: "Continue earlier work"}
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("the decision did not go to the native Marshal")
	}
	turns := tr.sentTurns()
	if len(turns) != 1 || !strings.HasPrefix(turns[0], "Decision request (untrusted description): ") || !strings.Contains(turns[0], req.Object) {
		t.Fatalf("turns: %q", turns)
	}
	var id string
	w.permissions.mu.Lock()
	for pid, p := range w.permissions.build.pending {
		if p.request.Key() == req.Key() {
			id = pid
			if p.paneID != nativeChatPane || p.provider != "claude" {
				t.Errorf("pending decision bound to %q/%q", p.paneID, p.provider)
			}
		}
	}
	w.permissions.mu.Unlock()
	if id == "" || !strings.Contains(turns[0], id) {
		t.Fatalf("no pending decision for the delivered request (id %q)", id)
	}
	// The Marshal answers (the decide proposal sets exactly this), and the
	// next queue pass applies it: the run is not stalled.
	allow := true
	w.permissions.mu.Lock()
	w.permissions.build.pending[id].answer = &allow
	w.permissions.build.pending[id].reason = "needed for the task"
	w.permissions.mu.Unlock()
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("the answered decision was not applied")
	}
	if !explicitReadGrant(rt, req.Object) {
		t.Fatal("the Marshal's allow did not reach the runtime")
	}
}

// A request that waited for a Marshal while none was live is delivered to
// the native chat once it runs.
func TestNativeChatTakesOverAwaitedDecisions(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-native-await", false)
	req := permission.Request{Kind: "marshal-command", Object: "/marshal settings control strict", Scope: "this project, next Marshal run", Who: "worker-1", Reason: "Apply the exact proposed setting"}
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("with no Marshal the request should wait")
	}
	tr := newFakeChatTransport()
	w.attachNativeChat(tr, w.providerRoot(), nil, nil, nil, nil, "", false)
	t.Cleanup(w.stopNativeChat)
	for _, queued := range w.permissions.queue.Take(false) {
		w.routeBuildPermission(t.Context(), queued)
	}
	turns := tr.sentTurns()
	if len(turns) != 1 || !strings.Contains(turns[0], "/marshal settings control strict") {
		t.Fatalf("awaited decision not delivered to the native chat: %q", turns)
	}
}
