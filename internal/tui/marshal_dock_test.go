package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/usage"
)

// dockFake is a fake tmux that keeps just enough pane state for the dock:
// which window each pane is in and its width. The Board is %0 in @0.
type dockFake struct {
	t    *testing.T
	dir  string
	log  string
	seen int
}

func newDockFake(t *testing.T, windowCols int) *dockFake {
	t.Helper()
	dir := t.TempDir()
	f := &dockFake{t: t, dir: dir, log: filepath.Join(dir, "argv.log")}
	script := fmt.Sprintf(`#!/bin/sh
d=%q
# Log on exit, after the effect: a test that sees a command logged may rely
# on its effect.
args="$*"
trap 'echo "$args" >> "$d/argv.log"' EXIT
target=""; prev=""; width=""; src=""
for a in "$@"; do
  case "$prev" in -t) target="$a";; -l|-x) width="$a";; -s) src="$a";; esac
  prev="$a"
done
key() { printf '%%s' "$1" | tr -d '%%'; }
case "$1" in
  display-message)
    case "$*" in
      *"#{pane_id} #{window_id} #{window_width} #{pane_width}"*)
        ww=$(cat "$d/winw")
        if [ "$target" = "%%0" ]; then
          printf '%%%%0 @0 %%s %%s\n' "$ww" "$ww"; exit 0
        fi
        k=$(key "$target")
        [ -f "$d/win_$k" ] || exit 1
        printf '%%s %%s %%s %%s\n' "$target" "$(cat "$d/win_$k")" "$ww" "$(cat "$d/w_$k")"
        exit 0;;
      *"#{session_id}"*) printf '$0\n'; exit 0;;
      *"#{session_name}:#{window_id}"*) printf 'test-session:@0\n'; exit 0;;
    esac;;
  join-pane) k=$(key "$src"); echo @0 > "$d/win_$k"; echo "$width" > "$d/w_$k";;
  break-pane) k=$(key "$src"); echo @9 > "$d/win_$k";;
  resize-pane) k=$(key "$target"); echo "$width" > "$d/w_$k";;
esac
exit 0
`, dir)
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f.setWindow(windowCols)
	tmux.SetBinaryPath(path)
	t.Cleanup(tmux.ResetBinaryPath)
	return f
}

func (f *dockFake) setWindow(cols int) {
	f.write("winw", strconv.Itoa(cols))
}

// pane creates a chat pane in its own window.
func (f *dockFake) pane(id, window string, width int) {
	k := strings.TrimPrefix(id, "%")
	f.write("win_"+k, window)
	f.write("w_"+k, strconv.Itoa(width))
}

func (f *dockFake) kill(id string) {
	_ = os.Remove(filepath.Join(f.dir, "win_"+strings.TrimPrefix(id, "%")))
}

func (f *dockFake) write(name, value string) {
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(value+"\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// take returns the complete commands logged since the last take. The log is
// only appended to, so a command the fake is still writing is never lost.
func (f *dockFake) take() string {
	data, _ := os.ReadFile(f.log)
	data = data[min(f.seen, len(data)):]
	if i := strings.LastIndexByte(string(data), '\n'); i >= 0 {
		data = data[:i+1]
	} else {
		data = nil
	}
	f.seen += len(data)
	return string(data)
}

func dockWorkspace(t *testing.T) (*Workspace, *activeTmuxAgent) {
	t.Helper()
	t.Setenv("TMUX", "/tmp/tmux-dock-test/default,1,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")
	w := NewWorkspace(nil, "test-proj", "test-session")
	w.workDir = t.TempDir()
	w.tmuxSession = fixtureTmuxNamespace(t, w.workDir)
	w.tmuxMarshalPaneID = "%0"
	chat := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: "%7", window: "marshal-chat-x", windowID: "@1"}
	w.tmuxMu.Lock()
	w.tmuxActiveWins[chat.id] = chat
	w.tmuxMu.Unlock()
	w.tmuxPath = "tmux"
	w.dock.enabled = true
	return w, chat
}

func mustContain(t *testing.T, log string, want ...string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(log, s) {
			t.Fatalf("missing %q in tmux calls:\n%s", s, log)
		}
	}
}

func mustNotContain(t *testing.T, log string, bad ...string) {
	t.Helper()
	for _, s := range bad {
		if strings.Contains(log, s) {
			t.Fatalf("unexpected %q in tmux calls:\n%s", s, log)
		}
	}
}

func TestMarshalDockJoinsFollowsResizeAndUndocks(t *testing.T) {
	f := newDockFake(t, 200)
	w, chat := dockWorkspace(t)
	f.pane("%7", "@1", 200)
	ctx := context.Background()
	dock, keys := dockKeyTable(w.providerRoot()), workspaceKeyTable(w.providerRoot())

	if err := w.reconcileDock(ctx); err != nil {
		t.Fatal(err)
	}
	log := f.take()
	mustContain(t, log,
		"set-option -p -t %7 remain-on-exit on",
		"join-pane -h -b -d -l 99 -s %7 -t %0",
		"set-option -w -t %0 pane-border-status top",
		"set-option -w -t %0 pane-border-format",
		"set-option -p -t %7 @marshal_dock_title",
		"set-option -p -t %7 @marshal_key_table "+dock,
		// F11 and Alt+Right in the chat only select the Board: the Board
		// never reads a switch key, so it cannot bounce focus back.
		"bind-key -T "+dock+" F11 if-shell -F #{==:#{@marshal_key_table},"+dock+"} select-pane -t %0 send-keys F11",
		"bind-key -T "+dock+" M-Right if-shell -F #{==:#{@marshal_key_table},"+dock+"} select-pane -t %0 send-keys M-Right",
		// The Board pane's own table while docked: F11 and Alt+Left select the chat.
		"set-option -p -t %0 @marshal_key_table "+boardDockKeyTable(w.providerRoot()),
		"bind-key -T "+boardDockKeyTable(w.providerRoot())+" F11 if-shell -F #{==:#{@marshal_key_table},"+boardDockKeyTable(w.providerRoot())+"} select-pane -t %7 send-keys F11",
		"bind-key -T "+boardDockKeyTable(w.providerRoot())+" M-Left if-shell -F #{==:#{@marshal_key_table},"+boardDockKeyTable(w.providerRoot())+"} select-pane -t %7 send-keys M-Left",
		// Both panes are titled on the border row, the selected one highlighted.
		"set-option -w -t %0 pane-active-border-style",
		"set-option -t %0 mouse on",
	)
	// Only the control keys are bound: everything else typed in the chat
	// reaches the chat, and Alt+Left stays the CLI's word move.
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "bind-key") {
			fields := strings.Fields(line)
			allowed := []string{"F7", "F8", "F9", "F11", "F12", "C-x", "M-x"}
			if fields[2] == dock {
				allowed = append(allowed, "M-Right")
			} else {
				allowed = append(allowed, "M-Left")
			}
			if !slices.Contains(allowed, fields[3]) {
				t.Fatalf("dock binds an ordinary key: %s", line)
			}
		}
	}
	if w.boardLayoutMode() != layout.Split || w.dockedChat() != "%7" || !chat.isJoined {
		t.Fatal("dock state not recorded")
	}

	// Nothing changed: only the geometry is read.
	if err := w.reconcileDock(ctx); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, f.take(), "join-pane", "resize-pane", "bind-key", "pane-border-status", "pane-border-format", "@marshal_dock_title")

	// The window grows: the chat's width follows SplitWidths.
	f.setWindow(151)
	if err := w.reconcileDock(ctx); err != nil {
		t.Fatal(err)
	}
	log = f.take()
	mustContain(t, log, "resize-pane -t %7 -x 75")
	mustNotContain(t, log, "join-pane")

	// /view focus breaks workers out, never the docked chat.
	if _, err := w.handleViewCommand(ctx, []string{"focus"}); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, f.take(), "break-pane")

	// /view classic returns the chat to its own window and its keys to the
	// ordinary agent table; the dock's table is removed.
	w.setBoardView(true)
	if err := w.reconcileDock(ctx); err != nil {
		t.Fatal(err)
	}
	log = f.take()
	mustContain(t, log,
		"break-pane -d -s %7 -n marshal-chat-x",
		"set-option -wu -t %0 pane-border-status",
		"set-option -wu -t %0 pane-border-format",
		"set-option -pu -t %7 @marshal_dock_title",
		"set-option -p -t %7 @marshal_key_table "+keys,
		"unbind-key -q -a -T "+dock,
		"select-pane -t %0",
	)
	if w.boardLayoutMode() != layout.Home || w.dockedChat() != "" || chat.isJoined || chat.windowID != "@9" {
		t.Fatalf("undock state: mode=%s pane=%q joined=%v window=%s", w.boardLayoutMode(), w.dockedChat(), chat.isJoined, chat.windowID)
	}

	// /view board docks it again; exit undocks it.
	w.setBoardView(false)
	if err := w.reconcileDock(ctx); err != nil {
		t.Fatal(err)
	}
	mustContain(t, f.take(), "join-pane -h -b -d -l 75 -s %7 -t %0")
	w.endDock()
	mustContain(t, f.take(), "break-pane -d -s %7 -n marshal-chat-x", "unbind-key -q -a -T "+dock)
	if w.dockedChat() != "" {
		t.Fatal("exit left the chat docked")
	}
}

// With no chat docked, the reconciler keeps the session's mouse on while the
// Board is the screen, so a click still works; exit unsets it.
func TestMarshalDockMouseWhenUndocked(t *testing.T) {
	f := newDockFake(t, 120)
	w, _ := dockWorkspace(t)
	// No chat is docked: the Board stands alone in its MARSHAL session.
	w.tmuxMu.Lock()
	delete(w.tmuxActiveWins, "marshal-chat")
	w.tmuxMu.Unlock()
	w.screen = NewScreen(nil)
	w.homeBoard = true
	if err := w.reconcileDock(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustContain(t, f.take(), "set-option -t %0 mouse on")

	// On exit the dock is disabled before the reconcile, so the mouse is unset.
	w.endDock()
	mustContain(t, f.take(), "set-option -u -t %0 mouse")
}

func TestMarshalDockNarrowWindowKeepsChatDocked(t *testing.T) {
	for _, tc := range []struct{ cols, chat int }{{113, 56}, {112, 55}, {106, 49}, {97, 40}, {96, 47}, {80, 39}, {40, 19}, {2, 1}} {
		if got := dockWidth(tc.cols); got != tc.chat {
			t.Fatalf("dockWidth(%d) = %d, want %d", tc.cols, got, tc.chat)
		}
	}
	if _, _, ok := layout.SplitWidths(80); ok {
		t.Fatal("80 columns is expected to be below the split minimum")
	}
	f := newDockFake(t, 80)
	w, _ := dockWorkspace(t)
	f.pane("%7", "@1", 80)
	if err := w.reconcileDock(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustContain(t, f.take(), "join-pane -h -b -d -l 39 -s %7 -t %0")
	if w.boardLayoutMode() != layout.Split {
		t.Fatal("narrow dock did not put the Board in split mode")
	}
	// The Board's 40 columns are below its split minimum: it paints its
	// compact form, and no pane overlaps.
	l := layout.Compute(80-39-1, 30, w.boardLayoutMode())
	if _, ok := l.Pane(layout.Inbox); !ok {
		t.Fatalf("compact Board lost its inbox: %+v", l.Panes)
	}
}

// codexWindow is a Codex quota window 42% used (58% left), resetting in ~4d.
func codexWindow(now time.Time) usage.Window {
	return usage.Window{Label: "wk", LeftPercent: 58, ResetsAt: now.Add(98 * time.Hour)}
}

func TestMarshalDockTitleShowsLimit(t *testing.T) {
	f := newDockFake(t, 120)
	w, chat := dockWorkspace(t)
	f.pane(chat.paneID, "@1", 120)
	chat.provider = "codex"
	now := time.Now()
	w.storeUsage([]usage.Quota{{
		Provider: "codex", Known: true, AsOf: now,
		Windows: []usage.Window{codexWindow(now)},
	}})
	if err := w.reconcileDock(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustContain(t, f.take(),
		"set-option -p -t %7 @marshal_dock_title  MARSHAL · ⬡ codex ▓▓░ 58% left ·4d    F11 Board ")

	// The reading turns unknown: the title says so within the same reconcile.
	w.storeUsage([]usage.Quota{{Provider: "codex", Reason: usage.ReasonStale}})
	if err := w.reconcileDock(context.Background()); err != nil {
		t.Fatal(err)
	}
	log := f.take()
	mustContain(t, log, "⬡ codex limit unknown")
	// A title-only change joins, binds or restyles nothing.
	mustNotContain(t, log, "join-pane", "bind-key", "pane-border-status")

	// An identical reconcile writes no title: reconcileDock is idempotent.
	if err := w.reconcileDock(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, f.take(), "@marshal_dock_title")
}

func TestDockTitleFits(t *testing.T) {
	w := &Workspace{}
	chat := &activeTmuxAgent{provider: "codex"}
	now := time.Now()
	w.storeUsage([]usage.Quota{{
		Provider: "codex", Known: true, AsOf: now,
		Windows: []usage.Window{codexWindow(now)},
	}})
	for _, tc := range []struct {
		width int
		want  string
	}{
		{120, " MARSHAL · ⬡ codex ▓▓░ 58% left ·4d    F11 Board "},
		{45, " MARSHAL · ⬡ codex 58% left    F11 Board "},
		{20, " MARSHAL · ⬡ codex    F11 Board "},
	} {
		if got := w.dockTitle(chat, tc.width); got != tc.want {
			t.Fatalf("width %d:\n got %q\nwant %q", tc.width, got, tc.want)
		}
	}
}

func TestDockTitleCases(t *testing.T) {
	now := time.Now()
	codexKnown := func(stale bool) usage.Quota {
		return usage.Quota{Provider: "codex", Known: true, Stale: stale, AsOf: now,
			Windows: []usage.Window{codexWindow(now)}}
	}
	for _, tc := range []struct {
		name     string
		provider string
		ascii    bool
		qs       []usage.Quota
		read     bool
		want     string
	}{
		{"known", "codex", false, []usage.Quota{codexKnown(false)}, true, " MARSHAL · ⬡ codex ▓▓░ 58% left ·4d    F11 Board "},
		{"known stale", "codex", false, []usage.Quota{codexKnown(true)}, true, " MARSHAL · ⬡ codex ▓▓░ ~58% left ·4d    F11 Board "},
		{"unknown", "agy", false, []usage.Quota{{Provider: "agy", Reason: usage.NotExposed}}, true, " MARSHAL · ◆ agy limit unknown    F11 Board "},
		{"not read yet", "claude", false, nil, false, " MARSHAL · ✳ claude    F11 Board "},
		{"no glyph", "", false, nil, false, " MARSHAL · chat    F11 Board "},
		{"ascii", "codex", true, []usage.Quota{codexKnown(false)}, true, " MARSHAL . cx codex ==- 58% left .4d    F11 Board "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &Workspace{}
			if tc.ascii {
				w.theme = NewTheme(ThemeNoColor, false, false)
			}
			if tc.read {
				w.storeUsage(tc.qs)
			}
			chat := &activeTmuxAgent{provider: tc.provider}
			if got := w.dockTitle(chat, 0); got != tc.want {
				t.Fatalf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestUsageReadKicksDock(t *testing.T) {
	f := newDockFake(t, 120)
	w, chat := dockWorkspace(t)
	f.pane(chat.paneID, "@1", 120)
	chat.provider = "codex"
	now := time.Now()
	reads := 0
	w.usage.every = 5 * time.Millisecond
	w.usage.readFn = func(time.Time) []usage.Quota {
		reads++
		left := 58.0
		if reads%2 == 0 {
			left = 18.0
		}
		return []usage.Quota{{Provider: "codex", Known: true, AsOf: now,
			Windows: []usage.Window{{Label: "wk", LeftPercent: left, ResetsAt: now.Add(98 * time.Hour)}}}}
	}
	// The Board must be visible for startUsage to read.
	w.screen = NewScreen(nil)
	w.homeBoard = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.startDock(ctx)
	waitDockLog(t, f, "join-pane")
	w.startUsage(ctx)
	// Two different readings reach the title without any key press.
	waitDockLog(t, f,
		"@marshal_dock_title  MARSHAL · ⬡ codex ▓▓░ 58% left",
		"@marshal_dock_title  MARSHAL · ⬡ codex ▓░░ 18% left")
}

func TestMarshalDockRejoinsRecreatedChat(t *testing.T) {
	f := newDockFake(t, 200)
	w, chat := dockWorkspace(t)
	f.pane("%7", "@1", 200)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dock := dockKeyTable(w.providerRoot())

	w.startDock(ctx)
	waitDockLog(t, f, "join-pane -h -b -d -l 99 -s %7 -t %0")

	// The chat pane dies and is gone: the dock lets it go.
	f.kill("%7")
	w.kickDock()
	waitDockLog(t, f, "unbind-key -q -a -T "+dock)
	waitFor(t, func() bool { return w.dockedChat() == "" && w.boardLayoutMode() == layout.Home }, func() string {
		return fmt.Sprintf("docked=%q mode=%s", w.dockedChat(), w.boardLayoutMode())
	})

	// Its restart recreated the chat window with a new pane: it is joined
	// again and the dock's keys move to it.
	f.pane("%8", "@4", 200)
	w.tmuxMu.Lock()
	chat.paneID, chat.windowID = "%8", "@4"
	w.tmuxMu.Unlock()
	w.kickDock()
	waitDockLog(t, f, "join-pane -h -b -d -l 99 -s %8 -t %0", "set-option -p -t %8 @marshal_key_table "+dock)
	waitFor(t, func() bool { return w.dockedChat() == "%8" })

	// A chat respawned in place gets the agent table bound over the dock's;
	// invalidating makes the next pass bind the dock's keys again.
	w.invalidateDockKeys("%8")
	w.kickDock()
	waitDockLog(t, f, "set-option -p -t %8 @marshal_key_table "+dock)
}

func waitDockLog(t *testing.T, f *dockFake, want ...string) {
	t.Helper()
	var seen strings.Builder
	waitFor(t, func() bool {
		seen.WriteString(f.take())
		for _, s := range want {
			if !strings.Contains(seen.String(), s) {
				return false
			}
		}
		return true
	}, func() string { return "tmux calls:\n" + seen.String() })
}

func waitFor(t *testing.T, ok func() bool, why ...func() string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			msg := "timed out"
			for _, w := range why {
				msg += "\n" + w()
			}
			t.Fatal(msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMarshalKeyFocusesDockedChat(t *testing.T) {
	f := newDockFake(t, 200)
	w, _ := dockWorkspace(t)
	f.pane("%7", "@1", 200)
	ctx := context.Background()
	// No Board on screen: F11 is left to tmux and focuses nothing here.
	if w.handleTerminalKey(ctx, KeyEvent{Type: KeyF11}) {
		t.Fatal("F11 asked the workspace to exit without a Board")
	}
	mustNotContain(t, f.take(), "select-pane -t %7")
	w.screen = NewScreen(nil)
	w.mu.Lock()
	w.state.Marshal = &MarshalPanel{RunID: "run"}
	w.mu.Unlock()
	for range 3 {
		w.handleTerminalKey(ctx, KeyEvent{Type: KeyF11})
		log := f.take()
		mustContain(t, log, "select-pane -t %7")
		mustNotContain(t, log, "break-pane")
		if w.dockedChat() != "%7" {
			t.Fatal("F11 closed the dock")
		}
	}
	// Esc is not a dock key either.
	w.handleShortcut(ctx, KeyEvent{Type: KeyEsc})
	mustNotContain(t, f.take(), "break-pane")
}
