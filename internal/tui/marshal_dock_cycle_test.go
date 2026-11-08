//go:build linux

package tui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

// TestRealTmuxDockFocusCyclesDoNotLeak moves focus between the Board and the
// docked Marshal chat 100 times the way the operator does (m through the key
// path, F11 typed on an attached client) while the reconciler is kicked on
// every cycle, then checks that nothing accumulated: the tmux server holds
// the same panes, windows and dock key bindings, the chat is still docked at
// the same width, and the process has no more goroutines or open files than
// after the first cycle.
func TestRealTmuxDockFocusCyclesDoNotLeak(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if testing.Short() {
		t.Skip("100 real tmux focus cycles")
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
	setWinsize(slave, 41, 120)
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
	run := func(args ...string) string {
		out, err := tmux.RunCommand(ctx, args...)
		if err != nil {
			t.Fatalf("tmux %v: %v", args, err)
		}
		return string(out)
	}
	layoutOf := func() string { return run("list-panes", "-t", board, "-F", "#{pane_id} #{pane_left} #{pane_width}") }
	docked := chatPane + " 0 " + strconv.Itoa(dockWidth(120)) + "\n"
	waitFor(t, func() bool { return strings.HasPrefix(layoutOf(), docked) }, layoutOf)
	active := func() string { return strings.TrimSpace(run("display-message", "-p", "-t", w.tmuxSession, "#{pane_id}")) }
	cycle := func() {
		w.kickDock()
		if w.handleTerminalKey(loopCtx, KeyEvent{Type: KeyRune, Rune: 'm'}) {
			t.Fatal("m asked the workspace to exit")
		}
		waitFor(t, func() bool { return active() == chatPane }, active)
		if _, err := master.Write([]byte("\x1b[23~")); err != nil { // F11
			t.Fatal(err)
		}
		waitFor(t, func() bool { return active() == board }, active)
	}
	type snapshot struct {
		panes, windows, keys string
		goroutines, fds      int
	}
	take := func() snapshot {
		return snapshot{
			panes:      run("list-panes", "-a", "-F", "#{pane_id} #{window_id} #{pane_width}"),
			windows:    run("list-windows", "-a", "-F", "#{window_id} #{window_name}"),
			keys:       run("list-keys", "-T", dockKeyTable(w.workDir)),
			goroutines: runtime.NumGoroutine(),
			fds:        openFiles(t),
		}
	}

	// One warm-up cycle starts whatever the key path starts once.
	cycle()
	waitFor(t, func() bool { return strings.HasPrefix(layoutOf(), docked) }, layoutOf)
	before := take()
	for range 100 {
		cycle()
	}
	waitFor(t, func() bool { return strings.HasPrefix(layoutOf(), docked) }, layoutOf)
	// Goroutines started by a cycle (tmux command waits, the kick) end on
	// their own; give them a moment before counting.
	var after snapshot
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		after = take()
		if after.goroutines <= before.goroutines+2 && after.fds <= before.fds+2 || time.Now().After(deadline) {
			break
		}
	}
	t.Logf("after 100 cycles: goroutines %d -> %d, open files %d -> %d", before.goroutines, after.goroutines, before.fds, after.fds)
	if after.panes != before.panes {
		t.Fatalf("panes changed over 100 focus cycles:\nbefore:\n%s\nafter:\n%s", before.panes, after.panes)
	}
	if after.windows != before.windows {
		t.Fatalf("windows changed over 100 focus cycles:\nbefore:\n%s\nafter:\n%s", before.windows, after.windows)
	}
	if after.keys != before.keys {
		t.Fatalf("dock key bindings changed over 100 focus cycles:\nbefore:\n%s\nafter:\n%s", before.keys, after.keys)
	}
	if after.goroutines > before.goroutines+2 {
		t.Fatalf("goroutines grew over 100 focus cycles: %d -> %d", before.goroutines, after.goroutines)
	}
	if after.fds > before.fds+2 {
		t.Fatalf("open files grew over 100 focus cycles: %d -> %d", before.fds, after.fds)
	}

	cancel()
	w.endDock()
	if got := strings.TrimSpace(run("list-panes", "-t", board, "-F", "#{pane_id}")); got != board {
		t.Fatalf("board window after exit: %q", got)
	}
	w.Close()
}

func openFiles(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}
