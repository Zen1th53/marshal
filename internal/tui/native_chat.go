package tui

// The Board's native Marshal chat (TUI3-0013, phase 2). It is opt-in: the
// tmux-docked provider pane stays the default until the owner switches with
// /view chat native, which is persisted per project. The native chat is
// docked at the left edge exactly where the tmux dock sits, so the owner
// rule "the Marshal chat is always docked and visible" holds in both modes.
//
// The provider binary is launched with flags only (chattransport). Its
// permission prompts reach the operator only through the Board inbox and
// are answered only through decidePermission, which records the same
// PERMISSION_DECIDED audit as every other inbox decision.

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/chattransport"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/nativehistory"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/chat"
)

// chatAsker is the transport side of the permission broker.
type chatAsker interface {
	Ask(context.Context, chattransport.PermissionAsk) (chattransport.Decision, error)
}

type nativeChatState struct {
	mu       sync.Mutex
	view     chat.View
	tr       chattransport.Transport
	provider string
	root     string
	model    string
	status   string
	dir      *briefingDir
	cleanup  func()
	cancel   context.CancelFunc
	asks     map[string]chattransport.PermissionAsk // inbox request key → ask
	watch    *nativeHistoryWatch
	syncing  atomic.Bool

	// runGrants are the operator's "allow for run" (A) answers: run id and
	// tool name. Like the run egress allowlist they are keyed by the run and
	// die with it; a later run asks again.
	runGrants map[string]bool

	wanted atomic.Bool // /view chat native, cached from the project setting
	loaded atomic.Bool
}

// Seams: tests replace the probe and the transport with fakes so no real
// provider session ever starts.
var (
	nativeChatProbe = func(ctx context.Context, provider, binary string) error {
		return chattransport.Probe(ctx, provider, binary, nil)
	}
	nativeChatTransport = func(provider string) (chattransport.Transport, error) {
		return chattransport.New(provider, nil)
	}
	nativeChatHistoryDir = func(root string) (string, error) {
		return nativehistory.HistoryDir(nativehistory.Claude, root)
	}
)

// chatViewSetting is the per-project setting key: "native" or "tmux".
const chatViewSetting = "tui-chat-view"

func loadChatView(root string) string {
	if root == "" || statepath.CheckPath(root, statepath.Path(root, chatViewSetting)) != nil {
		return "tmux"
	}
	data, err := os.ReadFile(statepath.Path(root, chatViewSetting))
	if err == nil && strings.TrimSpace(string(data)) == "native" {
		return "native"
	}
	return "tmux"
}

func saveChatView(root, mode string) error {
	if root == "" {
		return errors.New("no project is open")
	}
	path := statepath.Path(root, chatViewSetting)
	if err := statepath.CheckPath(root, path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(mode+"\n"), 0o600)
}

// nativeChatWanted reports the operator's chat view choice.
func (w *Workspace) nativeChatWanted() bool {
	if !w.chat.loaded.Load() {
		w.chat.wanted.Store(loadChatView(w.providerRoot()) == "native")
		w.chat.loaded.Store(true)
	}
	return w.chat.wanted.Load()
}

func (w *Workspace) nativeChatActive() bool {
	w.chat.mu.Lock()
	defer w.chat.mu.Unlock()
	return w.chat.tr != nil
}

// nativeChatShown reports whether this frame docks the native chat: it is
// running and the Board (not /view classic) owns the screen.
func (w *Workspace) nativeChatShown() bool {
	w.mu.RLock()
	classic := w.boardClassic
	w.mu.RUnlock()
	return !classic && w.nativeChatActive()
}

// nativeChatBound reports the native launch bound to dir, for the proposal
// lease. It must not be called with chat.mu held by the caller.
func (w *Workspace) nativeChatBound(dir *briefingDir, provider string) (string, bool) {
	w.chat.mu.Lock()
	defer w.chat.mu.Unlock()
	if w.chat.tr == nil || dir == nil || w.chat.dir != dir || canonicalNeutralProvider(w.chat.provider) != provider {
		return "", false
	}
	return w.chat.tr.SessionID(), true
}

func (w *Workspace) nativeChatAsker() chatAsker {
	w.chat.mu.Lock()
	defer w.chat.mu.Unlock()
	a, _ := w.chat.tr.(chatAsker)
	return a
}

// stripMarshalKickoff removes the opening turn runNativeAgent appended for
// the interactive CLI; the native chat sends it as its first message.
func stripMarshalKickoff(provider, root string, args []string) ([]string, string) {
	k := marshalKickoffArgs(provider, root)
	if len(k) != 1 || len(args) == 0 || args[len(args)-1] != k[0] {
		return args, ""
	}
	args = slices.Clone(args[:len(args)-1])
	if n := len(args); n > 0 && args[n-1] == "--" {
		args = args[:n-1]
	}
	return args, k[0]
}

// setClaudeMCPServerEnv sets (or with value "" removes) one variable of the
// MARSHAL MCP server in this launch's Claude MCP configuration.
func setClaudeMCPServerEnv(dir *briefingDir, key, value string) error {
	if dir == nil {
		return errors.New("Marshal MCP briefing directory unavailable")
	}
	path := filepath.Join(dir.path, "claude_mcp.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	server, _ := servers["marshal"].(map[string]any)
	if server == nil {
		return errors.New("Marshal MCP server config unavailable")
	}
	env, _ := server["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	if value == "" {
		delete(env, key)
	} else {
		env[key] = value
	}
	server["env"] = env
	data, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// chatProcessEnv is the provider's environment: the TUI's own without the
// tmux client variables (the Marshal must never reach MARSHAL's tmux), plus
// the launch's briefing variables.
func chatProcessEnv(extra []string) []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") {
			continue
		}
		env = append(env, e)
	}
	return append(env, extra...)
}

// launchNativeChat starts the native chat with the launch runNativeAgent
// prepared. An ErrUnqualified error means nothing was changed and the
// caller keeps the tmux chat.
func (w *Workspace) launchNativeChat(ctx context.Context, provider, root, binary string, args, env []string, dir *briefingDir, watch *nativeHistoryWatch) (string, error) {
	if canonicalNeutralProvider(provider) != "claude" {
		_, err := chattransport.New(canonicalNeutralProvider(provider), nil)
		return "", err
	}
	if err := nativeChatProbe(ctx, "claude", binary); err != nil {
		return "", err
	}
	tr, err := nativeChatTransport("claude")
	if err != nil {
		return "", err
	}
	brokerPath, closeBroker, err := w.startChatPermissionBroker(root)
	if err != nil {
		return "", err
	}
	const key = "MARSHAL_CHAT_PERMISSION_CONFIG"
	if err := setClaudeMCPServerEnv(dir, key, brokerPath); err != nil {
		closeBroker()
		return "", marshalNotStarted(provider, fmt.Errorf("chat permission binding failed: %w", err))
	}
	// From here the launch belongs to the native chat; the tmux chat, if a
	// surviving one is still running, must not answer as a second Marshal.
	if err := w.stopTmuxChat(ctx); err != nil {
		_ = setClaudeMCPServerEnv(dir, key, "")
		closeBroker()
		return "", marshalNotStarted(provider, fmt.Errorf("the tmux chat could not be closed: %w", err))
	}
	args, opening := stripMarshalKickoff(provider, root, args)
	saved := loadChatBinding(root)
	resume := ""
	if canonicalNeutralProvider(saved.Provider) == "claude" {
		resume = saved.SessionID
	}
	if watch != nil {
		if _, err := w.prepareChatHistoryWatch(root, watch, resume); err != nil {
			w.RecordActivity("Marshal chat history: " + err.Error())
		}
	}
	if err := tr.Start(ctx, chattransport.Session{Binary: binary, Dir: root, Args: args, Env: chatProcessEnv(env), SessionID: resume}); err != nil {
		_ = setClaudeMCPServerEnv(dir, key, "")
		closeBroker()
		if errors.Is(err, chattransport.ErrUnqualified) {
			return "", err
		}
		return "", marshalNotStarted(provider, fmt.Errorf("%w", err))
	}
	history := loadNativeChatHistory(root, resume, 200)

	note := ""
	switch {
	case resume != "":
		note = "Session " + shortID(resume) + " resumed."
	case opening != "":
		note = "Session opened."
	}
	w.attachNativeChat(tr, root, dir, watch, closeBroker, history, note, resume == "" && opening != "")
	if resume == "" && opening != "" {
		if err := tr.Send(ctx, chattransport.Turn{Text: opening}); err != nil {
			w.chatNote(chat.Error, err.Error())
		}
	}
	w.activateNativeProposalMCP(dir)
	runID := saved.RunID
	if m := w.marshalSession(); m != nil {
		m.mu.Lock()
		if m.runID != "" {
			runID = m.runID
		}
		m.mu.Unlock()
	}
	saved.Provider, saved.Binary, saved.SessionID, saved.RunID = provider, binary, resume, runID
	if err := saveChatBinding(root, saved); err != nil {
		w.RecordActivity("Marshal chat binding: " + err.Error())
	}
	w.ensureMarshalDraftWatch(root, provider, runID)
	w.kickDock()
	w.requestRepaint()
	return "Marshal chat: native, docked on the left of the Board. m talks to it; Esc returns to the Board.", nil
}

// attachNativeChat makes tr the docked chat and starts its pumps.
func (w *Workspace) attachNativeChat(tr chattransport.Transport, root string, dir *briefingDir, watch *nativeHistoryWatch, cleanup func(), history []chat.Entry, note string, thinking bool) {
	pumps, cancel := context.WithCancel(context.Background())
	w.chat.mu.Lock()
	w.chat.tr, w.chat.provider, w.chat.root, w.chat.dir = tr, "claude", root, dir
	w.chat.cleanup, w.chat.cancel, w.chat.watch = cleanup, cancel, watch
	w.chat.asks = map[string]chattransport.PermissionAsk{}
	w.chat.model, w.chat.status = "", "starting"
	w.chat.view = chat.View{}
	for _, e := range history {
		w.chat.view.Append(e)
	}
	if note != "" {
		w.chat.view.Append(chat.Entry{Role: chat.Note, At: time.Now(), Text: note})
	}
	w.chat.view.SetThinking(thinking)
	w.chat.mu.Unlock()
	w.startBackground(pumps, func(ctx context.Context) { w.pumpChatEvents(ctx, tr) })
	w.startBackground(pumps, func(ctx context.Context) { w.pumpChatPermissions(ctx, tr) })
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

var nativeSessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// loadNativeChatHistory reads the resumed session's conversation from the
// provider's own native history, which is where the transcript persists.
func loadNativeChatHistory(root, sessionID string, limit int) []chat.Entry {
	if !nativeSessionID.MatchString(sessionID) {
		return nil
	}
	dir, err := nativeChatHistoryDir(root)
	if err != nil {
		return nil
	}
	f, err := os.Open(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []chat.Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		tr, err := (importer.ClaudeJSONLAdapter{}).Decode(sc.Bytes())
		if err != nil || (tr.SessionID != "" && tr.SessionID != sessionID) {
			continue
		}
		for _, m := range tr.Messages {
			if strings.TrimSpace(m.Content) == "" || (m.Kind != "" && m.Kind != "text") {
				continue
			}
			role := chat.Marshal
			if m.Role == "user" {
				role = chat.You
			} else if m.Role != "assistant" {
				continue
			}
			at := m.Timestamp
			if !at.IsZero() {
				at = at.Local()
			}
			out = append(out, chat.Entry{Role: role, At: at, Text: m.Content})
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (w *Workspace) pumpChatEvents(ctx context.Context, tr chattransport.Transport) {
	events := tr.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-events:
			w.applyChatEvent(e)
		}
	}
}

// applyChatEvent feeds one event to the widget and repaints only when the
// widget changed: an idle chat costs no frames.
func (w *Workspace) applyChatEvent(e chattransport.Event) {
	now := time.Now()
	w.chat.mu.Lock()
	if w.chat.tr == nil {
		w.chat.mu.Unlock()
		return
	}
	v := &w.chat.view
	before := v.Version()
	turnEnded := false
	switch e.Kind {
	case chattransport.EventSession:
		w.chat.model, w.chat.status = e.Model, "live"
	case chattransport.EventTextDelta:
		v.SetThinking(true)
		v.Delta(now, e.MessageID, e.Text)
	case chattransport.EventText:
		v.Final(now, e.MessageID, e.Text)
	case chattransport.EventToolUse:
		v.ToolUse(now, e.ToolID, e.ToolName, e.ToolInput)
	case chattransport.EventToolResult:
		v.ToolResult(e.ToolID, e.Text, e.IsError)
	case chattransport.EventResult:
		v.EndTurn()
		if e.IsError {
			v.Append(chat.Entry{Role: chat.Error, At: now, Text: e.Text})
		}
		turnEnded = true
	case chattransport.EventError:
		v.Append(chat.Entry{Role: chat.Error, At: now, Text: e.Text})
	case chattransport.EventExit:
		v.EndTurn()
		w.chat.status = "exited"
		v.Append(chat.Entry{Role: chat.Note, At: now, Text: e.Text + "; the next message resumes the session."})
		turnEnded = true
	}
	changed := v.Version() != before
	root, watch := w.chat.root, w.chat.watch
	w.chat.mu.Unlock()
	// The result repeats the session id: a binding another writer saved
	// between init and the end of the turn is corrected here.
	if (e.Kind == chattransport.EventSession || e.Kind == chattransport.EventResult) && e.SessionID != "" {
		saved := loadChatBinding(root)
		if saved.SessionID != e.SessionID {
			saved.Provider, saved.SessionID = "claude", e.SessionID
			if err := saveChatBinding(root, saved); err != nil {
				w.RecordActivity("Marshal chat binding: " + err.Error())
			}
		}
		changed = changed || e.Kind == chattransport.EventSession
	}
	if turnEnded && watch != nil && w.chat.syncing.CompareAndSwap(false, true) {
		// The provider wrote the turn to its native history; capture it the
		// way the tmux chat's monitor does, once per turn, never on a timer.
		go func() {
			defer w.chat.syncing.Store(false)
			if err := watch.sync(); err != nil {
				w.RecordActivity("Marshal chat history: " + err.Error())
			}
		}()
	}
	if changed {
		w.requestRepaint()
	}
}

func (w *Workspace) chatNote(role chat.Role, text string) {
	w.chat.mu.Lock()
	w.chat.view.Append(chat.Entry{Role: role, At: time.Now(), Text: text})
	w.chat.mu.Unlock()
	w.requestRepaint()
}

func (w *Workspace) pumpChatPermissions(ctx context.Context, tr chattransport.Transport) {
	perms := tr.Permissions()
	for {
		select {
		case <-ctx.Done():
			return
		case ask := <-perms:
			w.routeChatAsk(ask)
		}
	}
}

// chatAskRequest is the inbox request for one provider permission ask.
func (w *Workspace) chatAskRequest(ask chattransport.PermissionAsk) permission.Request {
	runID := ""
	if m := w.marshalSession(); m != nil {
		m.mu.Lock()
		runID = m.runID
		m.mu.Unlock()
	}
	tool := chattransport.OneLine(ask.ToolName, 40)
	object := tool
	if s := chattransport.OneLine(ask.Summary, 100); s != "" {
		object += " " + s
	}
	provider := ask.Provider
	if provider == "" {
		provider = "claude"
	}
	return permission.Request{
		Kind:            app.MarshalToolPermissionKind,
		Object:          object,
		Scope:           "this tool call",
		Who:             "Marshal (" + provider + ")",
		Reason:          "The Marshal asked to run " + tool + " in the native chat.",
		RunID:           runID,
		ApprovalBinding: chattransport.OneLine(ask.ID, 80),
	}
}

// nativeChatPane names the native chat where a decision request records the
// tmux pane it was typed into.
const nativeChatPane = "native:marshal-chat"

// nativeChatDecisionTarget is the provider of the running native chat.
func (w *Workspace) nativeChatDecisionTarget() (string, bool) {
	w.chat.mu.Lock()
	defer w.chat.mu.Unlock()
	if w.chat.tr == nil {
		return "", false
	}
	return w.chat.provider, true
}

// deliverNativeChatTurn sends a runtime message (a decision request) to the
// native chat as one user turn, where the tmux chat would have it typed.
func (w *Workspace) deliverNativeChatTurn(ctx context.Context, text string) error {
	w.chat.mu.Lock()
	tr := w.chat.tr
	if tr != nil {
		w.chat.view.Append(chat.Entry{Role: chat.Note, At: time.Now(), Text: "MARSHAL runtime: " + chattransport.OneLine(text, 300)})
		w.chat.view.SetThinking(true)
	}
	w.chat.mu.Unlock()
	if tr == nil {
		return errors.New("the native Marshal chat is not running")
	}
	w.requestRepaint()
	return tr.Send(ctx, chattransport.Turn{Text: text})
}

func runGrantKey(runID, tool string) string { return runID + "\x00" + tool }

// runGrantSource marks a decision taken by an earlier "allow for run".
const runGrantSource = "run grant; operator inbox action=A"

// routeChatAsk puts a provider permission ask in the Board inbox. It never
// goes through the permission queue, so it can never become a popup. A tool
// the operator allowed for this run is decided at once through the same
// canonical path, recorded as a run-grant decision.
func (w *Workspace) routeChatAsk(ask chattransport.PermissionAsk) {
	req := w.chatAskRequest(ask)
	key := req.Key()
	w.chat.mu.Lock()
	if w.chat.tr == nil {
		w.chat.mu.Unlock()
		return
	}
	w.chat.asks[key] = ask
	if req.RunID != "" && w.chat.runGrants[runGrantKey(req.RunID, ask.ToolName)] {
		w.chat.view.Append(chat.Entry{Role: chat.Note, At: time.Now(), Text: req.Object + " allowed for this run."})
		w.chat.mu.Unlock()
		source := fmt.Sprintf("%s; run=%s; tool=%s", runGrantSource, req.RunID, chattransport.OneLine(ask.ToolName, 40))
		w.startBackground(context.Background(), func(ctx context.Context) {
			if err := w.decidePermission(ctx, req, true, source); err != nil {
				w.chatNote(chat.Error, "Run grant not applied: "+err.Error())
			}
		})
		w.requestRepaint()
		return
	}
	w.chat.view.Append(chat.Entry{Role: chat.Note, At: time.Now(), Text: "Permission for " + req.Object + " is waiting in the inbox."})
	w.chat.mu.Unlock()
	w.permissions.mu.Lock()
	if w.permissions.outstanding == nil {
		w.permissions.outstanding = map[string]bool{}
	}
	if !w.permissions.outstanding[key] {
		w.permissions.outstanding[key] = true
		w.permissions.inbox = append(w.permissions.inbox, req)
	}
	w.permissions.mu.Unlock()
	w.RecordActivity("Marshal permission request: " + req.Object)
	w.requestRepaint()
}

// answerNativeChatAsk sends an inbox decision, already recorded by
// decidePermission, back to the provider.
func (w *Workspace) answerNativeChatAsk(ctx context.Context, req permission.Request, allow bool, source string) error {
	w.chat.mu.Lock()
	ask, ok := w.chat.asks[req.Key()]
	delete(w.chat.asks, req.Key())
	tr := w.chat.tr
	verb := "denied"
	if allow {
		verb = "allowed"
	}
	// A is "allow for run": later asks for this tool in this run are
	// decided by the grant (and still audited one by one).
	if ok && allow && strings.Contains(source, "; action=A;") {
		if req.RunID != "" {
			if w.chat.runGrants == nil {
				w.chat.runGrants = map[string]bool{}
			}
			w.chat.runGrants[runGrantKey(req.RunID, ask.ToolName)] = true
			verb = "allowed for run " + req.RunID
		} else {
			verb = "allowed once (no run is open to scope it to)"
		}
	}
	if ok && !strings.HasPrefix(source, runGrantSource) {
		w.chat.view.Append(chat.Entry{Role: chat.Note, At: time.Now(), Text: chattransport.OneLine(ask.ToolName, 40) + " " + verb + " in the inbox."})
	}
	w.chat.mu.Unlock()
	w.requestRepaint()
	if !ok || tr == nil {
		return errors.New("the Marshal no longer waits for this permission")
	}
	msg := ""
	if !allow {
		msg = "Denied by the operator in the MARSHAL inbox."
		if i := strings.Index(source, "; reason="); i >= 0 {
			msg += " Reason: " + chattransport.OneLine(source[i+len("; reason="):], 300)
		}
	}
	return tr.Decide(ctx, ask, chattransport.Decision{Allow: allow, Message: msg})
}

// stopNativeChat ends the native chat and withdraws its open inbox asks.
func (w *Workspace) stopNativeChat() {
	w.chat.mu.Lock()
	tr, cancel, cleanup, dir, watch := w.chat.tr, w.chat.cancel, w.chat.cleanup, w.chat.dir, w.chat.watch
	keys := make(map[string]bool, len(w.chat.asks))
	for k := range w.chat.asks {
		keys[k] = true
	}
	w.chat.tr, w.chat.cancel, w.chat.cleanup, w.chat.dir, w.chat.watch, w.chat.asks = nil, nil, nil, nil, nil, nil
	w.chat.view.SetFocused(false)
	w.chat.mu.Unlock()
	if tr == nil {
		return
	}
	if cancel != nil {
		cancel()
	}
	_ = tr.Close()
	if cleanup != nil {
		cleanup()
	}
	w.revokeNativeProposalMCP(dir)
	dir.remove()
	w.permissions.mu.Lock()
	w.permissions.inbox = slices.DeleteFunc(w.permissions.inbox, func(r permission.Request) bool { return keys[r.Key()] })
	for k := range keys {
		delete(w.permissions.outstanding, k)
	}
	w.permissions.mu.Unlock()
	if watch != nil && w.chat.syncing.CompareAndSwap(false, true) {
		if err := watch.sync(); err != nil {
			w.RecordActivity("Marshal chat history: " + err.Error())
		}
		w.chat.syncing.Store(false)
	}
	w.requestRepaint()
}

// stopTmuxChat closes the tmux chat the way /marshal model does when it
// replaces the provider: the monitor is joined first so recovery cannot
// respawn it.
func (w *Workspace) stopTmuxChat(ctx context.Context) error {
	w.tmuxMu.Lock()
	chatAgent := copyAgentLocked(w.tmuxActiveWins["marshal-chat"])
	w.tmuxMu.Unlock()
	if chatAgent == nil {
		return nil
	}
	if chatAgent.cancel != nil {
		chatAgent.cancel()
	}
	if chatAgent.doneChan != nil {
		select {
		case <-chatAgent.doneChan:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if isPaneID(chatAgent.paneID) {
		if err := tmux.KillPane(ctx, chatAgent.paneID); err != nil {
			return err
		}
	}
	w.tmuxMu.Lock()
	delete(w.tmuxActiveWins, "marshal-chat")
	w.tmuxMu.Unlock()
	w.revokeProposalMCP(chatAgent)
	chatAgent.briefingDir.remove()
	w.kickDock()
	return nil
}

// handleViewChat is /view chat [native|tmux].
func (w *Workspace) handleViewChat(ctx context.Context, args []string) (string, error) {
	root := w.providerRoot()
	if len(args) == 0 {
		mode := "tmux"
		if w.nativeChatWanted() {
			mode = "native"
		}
		active := "tmux pane"
		if w.nativeChatActive() {
			active = "native widget"
		}
		return fmt.Sprintf("Chat view: %s (now showing the %s). /view chat native|tmux switches.", mode, active), nil
	}
	switch strings.ToLower(args[0]) {
	case "native":
		if err := saveChatView(root, "native"); err != nil {
			return "", fmt.Errorf("chat view not saved: %w", err)
		}
		w.chat.wanted.Store(true)
		w.chat.loaded.Store(true)
		if w.nativeChatActive() {
			return "Chat view: native (already running).", nil
		}
		if w.runtime == nil {
			return "Chat view saved as native. It starts with the next Marshal launch in an initialized project.", nil
		}
		out, err := w.marshalChat(context.WithValue(ctx, tmuxBackgroundLaunchKey{}, true))
		if err != nil {
			return "", err
		}
		if !w.nativeChatActive() {
			return "Chat view saved as native, but the native chat is not qualified here; the tmux-docked chat stays. " + out, nil
		}
		return "Chat view: native. " + out, nil
	case "tmux":
		if err := saveChatView(root, "tmux"); err != nil {
			return "", fmt.Errorf("chat view not saved: %w", err)
		}
		w.chat.wanted.Store(false)
		w.chat.loaded.Store(true)
		if !w.nativeChatActive() {
			return "Chat view: tmux (already).", nil
		}
		w.stopNativeChat()
		if w.runtime != nil && w.isTmuxActive() {
			if _, err := w.marshalChat(context.WithValue(ctx, tmuxBackgroundLaunchKey{}, true)); err != nil {
				return "", fmt.Errorf("chat view saved as tmux, but the tmux chat did not start: %w", err)
			}
		}
		w.kickDock()
		return "Chat view: tmux. The provider's own terminal is docked on the left.", nil
	}
	return "Usage: /view chat [native|tmux]", nil
}

// chatStyle colours the widget with the Board palette.
func chatStyle(th *Theme) chat.Style {
	v := boardVisual{theme: th}
	fg := func(name string) func(string) string { return func(s string) string { return v.fg(name, s) } }
	return chat.Style{
		You: fg("working"), Marshal: func(s string) string { return v.bold(v.fg("accent", s)) },
		Dim: fg("dim"), Tool: fg("dim"), Fail: fg("failed"), Error: fg("failed"),
		Title: v.bold, Accent: fg("accent"), Unicode: th == nil || th.Unicode,
	}
}

// nativeChatFrame paints the chat in cols×rows.
func (w *Workspace) nativeChatFrame(th *Theme, cols, rows int) ([]string, int, int, bool) {
	w.chat.mu.Lock()
	defer w.chat.mu.Unlock()
	state := w.chat.status
	if w.chat.model != "" {
		state = w.chat.model
	}
	w.chat.view.SetHeader(chat.Header{Provider: w.chat.provider, Role: "native chat", State: state})
	lines, r, c := w.chat.view.Render(cols, rows, chatStyle(th))
	return lines, r, c, w.chat.view.Focused()
}

// frameLines composes the frame paint draws, with its cursor. With the
// native chat docked it splits the window as the tmux dock does: chat on
// the left, a rule, the Board on the right.
func (w *Workspace) frameLines(cols, rows int) ([]string, int, int) {
	if !w.nativeChatShown() || w.diffViewer.IsOpen() {
		return w.frameLinesFull(cols, rows)
	}
	chatW, boardW, _ := layout.SplitWidths(cols)
	if chatW < 1 || boardW < 1 {
		return w.frameLinesFull(cols, rows)
	}
	right, cr, cc := w.frameLinesFull(boardW, rows)
	w.mu.RLock()
	th := w.theme
	w.mu.RUnlock()
	left, lr, lc, focused := w.nativeChatFrame(th, chatW, rows)
	rule := "|"
	if th != nil {
		rule = th.Colorize(th.Border, th.BoxVert)
	}
	out := make([]string, rows)
	for i := range rows {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = PadCell(l, chatW) + rule + PadCell(r, boardW)
	}
	if focused {
		return out, lr, lc
	}
	return out, cr, cc + chatW + 1
}

// focusNativeChat moves the keyboard to (or away from) the chat composer.
func (w *Workspace) focusNativeChat(on bool) {
	w.chat.mu.Lock()
	w.chat.view.SetFocused(on)
	w.chat.mu.Unlock()
	w.requestRepaint()
}

func (w *Workspace) nativeChatFocused() bool {
	w.chat.mu.Lock()
	defer w.chat.mu.Unlock()
	return w.chat.tr != nil && w.chat.view.Focused()
}

// handleNativeChatKey owns the keyboard while the chat composer is focused.
// Keys it does not use (function keys, Ctrl+X, Ctrl+P) pass on.
func (w *Workspace) handleNativeChatKey(ctx context.Context, event KeyEvent) bool {
	if !w.nativeChatShown() || !w.nativeChatFocused() {
		return false
	}
	w.chat.mu.Lock()
	v := &w.chat.view
	tr := w.chat.tr
	handled := true
	var send, command, copied string
	switch event.Type {
	case KeyCtrlC:
		if v.Text() == "" {
			handled = false
		}
		v.Clear()
	case KeyEsc, KeyF11:
		v.SetFocused(false)
	case KeyEnter:
		text := v.Take()
		switch {
		case text == "":
		case strings.HasPrefix(text, "/"):
			command = text
		default:
			v.ScrollEnd()
			v.Append(chat.Entry{Role: chat.You, At: time.Now(), Text: text})
			v.SetThinking(true)
			send = text
		}
	case KeyRune:
		v.Insert(string(event.Rune))
	case KeyPaste:
		v.Insert(event.Paste)
	case KeyBackspace:
		v.Backspace()
	case KeyDelete:
		v.Delete()
	case KeyLeft:
		v.Move(-1)
	case KeyRight:
		v.Move(1)
	case KeyHome, KeyCtrlA:
		v.Home()
	case KeyEnd, KeyCtrlE:
		if v.Scrolled() > 0 && v.Text() == "" {
			v.ScrollEnd()
		} else {
			v.End()
		}
	case KeyCtrlU:
		v.Clear()
	case KeyUp:
		v.Scroll(1)
	case KeyDown:
		v.Scroll(-1)
	case KeyPgUp:
		v.Scroll(10)
	case KeyPgDn:
		v.Scroll(-10)
	case KeyMouse:
		// The wheel scrolls the transcript; clicks stay with the Board.
		switch event.MouseButton {
		case 64:
			v.Scroll(3)
		case 65:
			v.Scroll(-3)
		default:
			handled = false
		}
	case KeyCtrlY:
		copied = v.LastReply()
	default:
		handled = false
	}
	w.chat.mu.Unlock()
	if send != "" && tr != nil {
		w.startBackground(ctx, func(ctx context.Context) {
			if err := tr.Send(ctx, chattransport.Turn{Text: send}); err != nil {
				w.chatNote(chat.Error, err.Error())
			}
		})
	}
	if command != "" {
		w.runCommand(ctx, command)
	}
	if event.Type == KeyCtrlY {
		w.copyChatReply(ctx, copied)
	}
	return handled
}

// copyChatReply puts the Marshal's last reply on the clipboard: OSC 52 for
// the terminal, and tmux's paste buffer when MARSHAL runs inside tmux.
func (w *Workspace) copyChatReply(ctx context.Context, text string) {
	if strings.TrimSpace(text) == "" {
		w.chatNote(chat.Note, "Nothing to copy yet.")
		return
	}
	if w.terminal != nil && w.terminal.out != nil && w.terminal.IsTerminal() {
		_, _ = fmt.Fprintf(w.terminal.out, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
	}
	if w.isTmuxActive() {
		w.startBackground(ctx, func(ctx context.Context) {
			_, _ = tmux.RunCommand(ctx, "set-buffer", "--", text)
		})
	}
	w.chatNote(chat.Note, fmt.Sprintf("Copied the last reply (%d characters).", len([]rune(text))))
}

// chatTransportQualifiedEvent records, at /harness verify, whether the
// verified provider version lies in its native chat transport's qualified
// range. The launch probe applies the same range before any turn.
const chatTransportQualifiedEvent = "CHAT_TRANSPORT_QUALIFIED"

// recordChatTransportQualification appends the native chat transport
// contract for a verified provider and returns a one-line summary.
func (w *Workspace) recordChatTransportQualification(ctx context.Context, provider, version, evidence string) (string, error) {
	provider = canonicalNeutralProvider(provider)
	data := map[string]any{"provider": provider, "installed_version": version, "probe_evidence_id": evidence, "source": "harness verify"}
	var line string
	if provider == "claude" {
		r := chattransport.ClaudeRange
		data["transport"] = "claude -p stream-json in/out, --include-partial-messages, --permission-prompt-tool " + chattransport.ClaudePermissionTool
		data["range_min"], data["range_below"] = r.Min, r.Below
		if err := r.Qualify(version); err != nil {
			data["result"], data["reason"] = "refused", err.Error()
			line = fmt.Sprintf("Native chat transport: refused for %s %s (qualified >=%s <%s); the tmux-docked chat stays.", provider, version, r.Min, r.Below)
		} else {
			data["result"] = "qualified"
			line = fmt.Sprintf("Native chat transport: qualified for %s %s (stream-json, range >=%s <%s).", provider, version, r.Min, r.Below)
		}
	} else {
		data["result"], data["reason"] = "unsupported", "no structured chat transport is qualified for this provider"
		line = "Native chat transport: none qualified for " + provider + "; the tmux-docked chat stays."
	}
	if w.store == nil {
		return line, errors.New("store unavailable: native chat transport contract not recorded")
	}
	id, err := model.NewID("EVENT-CHAT-TRANSPORT-")
	if err != nil {
		return line, err
	}
	if err := w.store.AppendEvent(ctx, nil, model.Event{ID: id, Type: chatTransportQualifiedEvent, ProjectID: w.projectID, Timestamp: time.Now().UTC(), Data: data}); err != nil {
		return line, err
	}
	return line, nil
}
