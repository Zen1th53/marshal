//go:build linux

package tui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

// TestRealTmuxDockFocusSwitch docks a chat beside the Board on a private tmux
// server with an attached client and switches the keyboard between them the
// ways an operator does: F11 from either side, Alt+Right from the chat,
// Alt+Left from the Board, and a click on either pane. tmux handles every
// one of them, so neither pane's program ever reads the switch key, while
// Alt+arrow pointing away from the other pane still reaches the program as
// its word move. The selected pane's border title is highlighted.
func TestRealTmuxDockFocusSwitch(t *testing.T) {
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
	// Both panes show every key they receive.
	if _, err := tmux.RunCommand(ctx, "respawn-pane", "-k", "-t", board, "-c", work, "sh", "-c", "stty raw -echo; printf 'BOARD\\r\\n'; exec cat -v"); err != nil {
		t.Fatal(err)
	}
	master, slave := openPTY(t)
	setWinsize(slave, 41, 160)
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
		return err == nil && g.WindowWidth == 160
	})

	chatWindow := tmux.ChatWindowName(w.workDir)
	var chatPane string
	if err := tmux.NewWindow(ctx, w.tmuxSession, chatWindow, work, nil, []string{"sh", "-c", "stty raw -echo; printf 'CHAT\\r\\n'; exec cat -v"}, &chatPane); err != nil {
		t.Fatal(err)
	}
	chat := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: chatPane, window: chatWindow, provider: "codex"}
	w.tmuxMu.Lock()
	w.tmuxActiveWins[chat.id] = chat
	w.tmuxMu.Unlock()
	if err := w.bindWorkspaceKeys(ctx, board, w.workDir); err != nil {
		t.Fatal(err)
	}
	if err := w.bindWorkspaceKeys(ctx, chatPane, w.workDir); err != nil {
		t.Fatal(err)
	}
	// The operator's own setting may be off: the dock turns it on.
	if _, err := tmux.RunCommand(ctx, "set-option", "-g", "mouse", "off"); err != nil {
		t.Fatal(err)
	}
	w.dock.enabled = true
	if err := w.reconcileDock(ctx); err != nil {
		t.Fatal(err)
	}
	waitForTmuxOutput(t, board, "BOARD")
	waitForTmuxOutput(t, chatPane, "CHAT")

	display := func(target, format string) string {
		out, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", target, format)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	active := func() string { return display(w.tmuxSession, "#{pane_id}") }
	press := func(keys string) {
		t.Helper()
		if _, err := master.Write([]byte(keys)); err != nil {
			t.Fatal(err)
		}
	}
	const (
		f11      = "\x1b[23~"
		altLeft  = "\x1b[1;3D"
		altRight = "\x1b[1;3C"
	)
	if active() != board {
		t.Fatalf("the dock moved focus off the Board: %s", active())
	}
	if display(board, "#{mouse}") != "1" {
		t.Fatal("mouse focus is not on while docked")
	}

	// The highlighted title follows the selected pane.
	title := func(pane string) string { return display(pane, "#{E:pane-border-format}") }
	if got := title(board); !strings.Contains(got, "▶") || !strings.Contains(got, "BOARD") || !strings.Contains(got, "F11 / Alt+← chat") {
		t.Fatalf("board title while selected: %q", got)
	}
	if got := title(chatPane); strings.Contains(got, "▶") || !strings.Contains(got, "F11 / Alt+→ Board") {
		t.Fatalf("chat title while the Board is selected: %q", got)
	}

	for range 2 {
		// F11 from the Board selects the chat, F11 from the chat the Board.
		press(f11)
		waitFor(t, func() bool { return active() == chatPane })
		if got := title(chatPane); !strings.Contains(got, "▶") {
			t.Fatalf("chat title while selected: %q", got)
		}
		press(f11)
		waitFor(t, func() bool { return active() == board })
		// Alt+Left from the Board selects the chat on its left.
		press(altLeft)
		waitFor(t, func() bool { return active() == chatPane })
		// Alt+Left in the chat stays the CLI's: it reaches the chat.
		press(altLeft + "x")
		waitFor(t, func() bool { out, _ := tmux.CapturePane(ctx, chatPane); return strings.Contains(out, "Dx") }, func() string {
			out, _ := tmux.CapturePane(ctx, chatPane)
			b, _ := tmux.CapturePane(ctx, board)
			return "chat:\n" + out + "\nboard:\n" + b + "\nactive " + active() + " table " + display(w.tmuxSession, "#{client_key_table}")
		})
		// Alt+Right from the chat selects the Board.
		press(altRight)
		waitFor(t, func() bool { return active() == board })
		// Alt+Right on the Board stays the Board's.
		press(altRight + "y")
		waitForTmuxOutput(t, board, "Cy")
		// Clicks select the pane under the pointer: the chat is the left
		// half, the Board the right.
		press("\x1b[<0;20;10M\x1b[<0;20;10m")
		waitFor(t, func() bool { return active() == chatPane })
		press("\x1b[<0;120;10M\x1b[<0;120;10m")
		waitFor(t, func() bool { return active() == board })
	}
	for _, pane := range []string{board, chatPane} {
		out, err := tmux.CapturePane(ctx, pane)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "[23~") || strings.Contains(out, "C^[") {
			t.Fatalf("a switch key reached %s:\n%s", pane, out)
		}
		if strings.Contains(out, "[<0;") {
			t.Fatalf("a focus click reached %s as text:\n%s", pane, out)
		}
	}

	// Exit gives the Board its own F11 back and the session its mouse.
	w.endDock()
	if got := display(board, "#{@marshal_key_table}"); got != workspaceKeyTable(w.workDir) {
		t.Fatalf("board key table after undock: %q", got)
	}
	if keys, err := tmux.RunCommand(ctx, "list-keys", "-T", boardDockKeyTable(w.workDir)); err == nil && strings.TrimSpace(string(keys)) != "" {
		t.Fatalf("board dock key table survived the dock:\n%s", keys)
	}
	if display(board, "#{mouse}") != "0" {
		t.Fatal("mouse focus outlived the dock")
	}

}
