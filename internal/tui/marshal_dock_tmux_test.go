//go:build linux

package tui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// TestRealTmuxMarshalDock docks a chat window beside the Board window on a
// private tmux server with an attached client, then checks the pane layout,
// that keys typed in the chat reach it, and that F11 pressed on the client
// returns focus to the Board.
func TestRealTmuxMarshalDock(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	work := t.TempDir()

	panes, err := tmux.ListPanes(ctx, w.tmuxSession)
	if err != nil || len(panes) != 1 {
		t.Fatalf("board pane: %+v %v", panes, err)
	}
	board := panes[0].PaneID
	w.tmuxMarshalPaneID = board
	// The Board pane paints once the dock has given it its width.
	if _, err := tmux.RunCommand(ctx, "respawn-pane", "-k", "-t", board, "-c", work, "sh", "-c", "stty -echo; while [ ! -f go ]; do sleep 0.05; done; cat frame; exec sleep 600"); err != nil {
		t.Fatal(err)
	}

	// An attached client: key bindings only fire for keys a client types.
	master, slave := openPTY(t)
	setWinsize(slave, 51, 200) // 50 window rows above the status line
	client := exec.Command(w.tmuxPath, "attach-session", "-t", w.tmuxSession)
	client.Stdin, client.Stdout, client.Stderr = slave, slave, slave
	client.Env = append(os.Environ(), "TERM=xterm-256color")
	client.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Process.Kill(); _ = client.Wait() }()
	go io.Copy(io.Discard, master)
	waitFor(t, func() bool {
		g, err := tmux.QueryPaneGeometry(ctx, board)
		return err == nil && g.WindowWidth == 200
	})

	chatWindow := tmux.ChatWindowName(w.workDir)
	var chatPane string
	if err := tmux.NewWindow(ctx, w.tmuxSession, chatWindow, work, nil, []string{"sh", "-c", "printf 'Marshal chat (fixture pane)\\n> '; exec cat"}, &chatPane); err != nil {
		t.Fatal(err)
	}
	chat := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: chatPane, window: chatWindow}
	w.tmuxMu.Lock()
	w.tmuxActiveWins[chat.id] = chat
	w.tmuxMu.Unlock()
	// As at start-up: the Board and the chat carry the agent key table.
	if err := w.bindWorkspaceKeys(ctx, board, w.workDir); err != nil {
		t.Fatal(err)
	}
	if err := w.bindWorkspaceKeys(ctx, chatPane, w.workDir); err != nil {
		t.Fatal(err)
	}

	w.dock.enabled = true
	if err := w.reconcileDock(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := tmux.RunCommand(ctx, "list-panes", "-t", board, "-F", "#{pane_id} #{pane_left} #{pane_width} #{pane_height}")
	if err != nil {
		t.Fatal(err)
	}
	// The dock title row (pane-border-status top) takes one of the 50 window rows.
	want := chatPane + " 0 99 49\n" + board + " 100 100 49\n"
	if string(out) != want {
		t.Fatalf("dock layout:\n%s\nwant:\n%s", out, want)
	}
	if left, err := tmux.ListPanes(ctx, w.tmuxSession); err != nil || len(left) != 2 {
		t.Fatalf("panes after dock: %+v %v", left, err)
	}

	// The Board paints its split layout in the 100 columns it was given.
	now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
	b := demoBoard(now)
	th := NewTheme(ThemeDefault, true, true)
	v := boardVisual{board: b, now: now, theme: th, frame: anim.Frame{}, selectedTask: "T-023", selectedInbox: b.Inbox[0].Request.Key(), focus: layout.Tasks, edition: "Marshal"}
	frame := paintBoard(layout.Compute(100, 49, w.boardLayoutMode()), th, v.render)
	if err := os.WriteFile(filepath.Join(work, "frame"), []byte(strings.Join(frame, "\r\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	active := func() string {
		out, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", w.tmuxSession, "#{pane_id}")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	for i := range 3 {
		// m: focus moves to the chat; the dock stays.
		if err := w.focusMarshalChat(ctx); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return active() == chatPane })
		// Ordinary keys reach the chat.
		word := []string{"hello", "plan", "status"}[i]
		if _, err := master.Write([]byte(word)); err != nil {
			t.Fatal(err)
		}
		waitForTmuxOutput(t, chatPane, word)
		// F11 on the client returns to the Board pane.
		if _, err := master.Write([]byte("\x1b[23~")); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return active() == board })
	}

	if dir := os.Getenv("MARSHAL_DOCK_ANS"); dir != "" {
		writeDockANS(t, ctx, chatPane, board, filepath.Join(dir, "dock-200x50.ans"))
	}

	// Exit returns the chat to its own window and removes the binding.
	w.endDock()
	out, err = tmux.RunCommand(ctx, "list-panes", "-t", board, "-F", "#{pane_id}")
	if err != nil || strings.TrimSpace(string(out)) != board {
		t.Fatalf("board window after undock: %q %v", out, err)
	}
	g, err := tmux.QueryPaneGeometry(ctx, chatPane)
	if err != nil || g.PaneWidth != 200 {
		t.Fatalf("chat after undock: %+v %v", g, err)
	}
	name, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", chatPane, "#{window_name}")
	if err != nil || strings.TrimSpace(string(name)) != chatWindow {
		t.Fatalf("chat window name %q %v", name, err)
	}
	keys, err := tmux.RunCommand(ctx, "list-keys", "-T", dockKeyTable(w.workDir))
	if err == nil && strings.TrimSpace(string(keys)) != "" {
		t.Fatalf("dock key table survived the dock:\n%s", keys)
	}
	if active() != board {
		t.Fatal("undock moved focus off the Board")
	}
}

// writeDockANS composes the two docked panes side by side as the client sees
// them, with tmux's border column between them.
func writeDockANS(t *testing.T, ctx context.Context, chat, board, path string) {
	t.Helper()
	capture := func(pane string) []string {
		out, err := tmux.RunCommand(ctx, "capture-pane", "-e", "-p", "-t", pane)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	}
	waitForTmuxOutput(t, board, "T-023")
	left, right := capture(chat), capture(board)
	var b strings.Builder
	for y := range 49 {
		l, r := "", ""
		if y < len(left) {
			l = left[y]
		}
		if y < len(right) {
			r = right[y]
		}
		b.WriteString(l + "\x1b[0m" + strings.Repeat(" ", max(0, 99-VisibleLen(l))) + "\x1b[90m│\x1b[0m" + r + "\x1b[0m\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRealTmuxDockThroughTheKeyPath runs the dock as the raw terminal loop
// does: the reconciler goroutine docks the chat at the left of the Board's
// window on its own, m pressed through handleTerminalKey moves focus to
// the chat, F11 typed on an attached client returns it to the Board, and
// exit gives the chat its window back and stops the reconciler.
func TestRealTmuxDockThroughTheKeyPath(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	work := t.TempDir()

	panes, err := tmux.ListPanes(ctx, w.tmuxSession)
	if err != nil || len(panes) != 1 {
		t.Fatalf("board pane: %+v %v", panes, err)
	}
	board := panes[0].PaneID
	w.tmuxMarshalPaneID = board
	master, slave := openPTY(t)
	setWinsize(slave, 41, 120) // 40 window rows above the status line
	client := exec.Command(w.tmuxPath, "attach-session", "-t", w.tmuxSession)
	client.Stdin, client.Stdout, client.Stderr = slave, slave, slave
	client.Env = append(os.Environ(), "TERM=xterm-256color")
	client.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Process.Kill(); _ = client.Wait() }()
	go io.Copy(io.Discard, master)
	waitFor(t, func() bool {
		g, err := tmux.QueryPaneGeometry(ctx, board)
		return err == nil && g.WindowWidth == 120
	})

	chatWindow := tmux.ChatWindowName(w.workDir)
	var chatPane string
	if err := tmux.NewWindow(ctx, w.tmuxSession, chatWindow, work, nil, []string{"sh", "-c", "printf 'Marshal chat (fixture pane)\\n> '; exec cat"}, &chatPane); err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: chatPane, window: chatWindow}
	w.tmuxMu.Unlock()
	for _, pane := range []string{board, chatPane} {
		if err := w.bindWorkspaceKeys(ctx, pane, w.workDir); err != nil {
			t.Fatal(err)
		}
	}
	w.screen = NewScreen(nil)
	w.setMarshalPanel(newMarshalPanel("R-7f3a", "codex", fakeRun(), "running"))
	if !w.boardVisible() {
		t.Fatal("the run's Board is not on screen")
	}

	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w.startDock(loopCtx)
	layoutOf := func() string {
		out, err := tmux.RunCommand(ctx, "list-panes", "-t", board, "-F", "#{pane_id} #{pane_left} #{pane_width}")
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	want := chatPane + " 0 " + strconv.Itoa(dockWidth(120)) + "\n"
	waitFor(t, func() bool { return strings.HasPrefix(layoutOf(), want) }, func() string { return layoutOf() })
	// The reconciler marks the dock once the chat's keys are bound, after
	// tmux has moved the pane.
	waitFor(t, func() bool { return w.boardLayoutMode() == layout.Split })
	g, err := tmux.QueryPaneGeometry(ctx, board)
	if err != nil {
		t.Fatal(err)
	}
	lines, _, _ := w.frameLines(g.PaneWidth, 40)
	if plain := StripANSI(strings.Join(lines, "\n")); !strings.Contains(plain, "TASKS") || len(lines) != 40 {
		t.Fatalf("Board frame beside the chat:\n%s", plain)
	}

	active := func() string {
		out, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", w.tmuxSession, "#{pane_id}")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	for range 2 {
		if w.handleTerminalKey(loopCtx, KeyEvent{Type: KeyF11}) {
			t.Fatal("F11 asked the workspace to exit")
		}
		waitFor(t, func() bool { return active() == chatPane })
		if _, err := master.Write([]byte("\x1b[23~")); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return active() == board })
	}
	if !strings.HasPrefix(layoutOf(), want) {
		t.Fatalf("m or F11 moved the chat:\n%s", layoutOf())
	}

	cancel()
	w.endDock()
	out, err := tmux.RunCommand(ctx, "list-panes", "-t", board, "-F", "#{pane_id}")
	if err != nil || strings.TrimSpace(string(out)) != board {
		t.Fatalf("board window after exit: %q %v", out, err)
	}
	w.Close()
	if w.boardLayoutMode() != layout.Home {
		t.Fatal("the Board still paints beside a chat that left")
	}
}
