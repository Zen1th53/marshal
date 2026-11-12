package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// q on the "what next?" card quits, and the quit ends the tmux session
// MARSHAL made: the chat docked beside the Board and every worker view are
// closed and the Board's own pane closes with the process, so the attach
// that showed the session returns the operator to the shell. Before, the
// chat went back to its own window and stayed: with the Board gone tmux
// showed it full screen, and only detaching got the operator out.
func TestQuitFromNextCardEndsOwnedSession(t *testing.T) {
	f := newDockFake(t, 200)
	w, _ := dockWorkspace(t)
	w.theme = NewTheme(ThemeNoColor, true, false)
	w.screen = NewScreen(NewTerminal(strings.NewReader(""), &strings.Builder{}))
	w.homeBoard = true
	f.pane("%7", "@1", 200)
	f.pane("%8", "@2", 200)
	w.tmuxMu.Lock()
	w.tmuxActiveWins["task-a"] = &activeTmuxAgent{id: "task-a", role: "task", paneID: "%8", window: "marshal-task-a", windowID: "@2"}
	w.tmuxMu.Unlock()
	ctx := context.Background()
	if err := w.reconcileDock(ctx); err != nil {
		t.Fatal(err)
	}
	f.take()
	f.write("owned", "1")
	f.write("panes", strings.Join([]string{
		"%0\t@0\tmarshal\t1\t0\t0\t",
		"%7\t@1\tmarshal-chat-x\t2\t0\t0\t",
		"%8\t@2\tmarshal-task-a\t3\t0\t0\t",
	}, "\n"))

	w.openNext(boardmodel.RunHead{ID: "run", State: marshal.Closed, Done: 1, Total: 1})
	if !w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 'q'}) {
		t.Fatal("q on the what-next card did not quit")
	}
	// The raw terminal loop's exit, in its order: the dock returns the chat,
	// Close stops the workers, then the owned session ends.
	w.endDock()
	w.Close()
	w.endOwnedSession()
	log := f.take()
	mustContain(t, log,
		"show-options -qv -t ="+w.tmuxSession+": @marshal_owned",
		"set-option -p -t %0 remain-on-exit off",
		"kill-pane -t %7",
		"kill-pane -t %8",
	)
	mustNotContain(t, log, "kill-pane -t %0", "kill-session")
	w.tmuxMu.Lock()
	left := len(w.tmuxActiveWins)
	w.tmuxMu.Unlock()
	if left != 0 {
		t.Fatalf("%d agents still listed as running after the session ended", left)
	}
}

// A session MARSHAL did not create is never ended: the quit leaves the
// operator's own windows, and the chat in its own window, as they were.
func TestQuitLeavesSessionNotOwned(t *testing.T) {
	f := newDockFake(t, 200)
	w, _ := dockWorkspace(t)
	f.pane("%7", "@1", 200)
	f.write("panes", "%0\t@0\tmarshal\t1\t0\t0\t\n%7\t@1\tmarshal-chat-x\t2\t0\t0\t\n")
	w.endDock()
	w.Close()
	w.endOwnedSession()
	mustNotContain(t, f.take(), "kill-pane", "kill-session", "remain-on-exit off")
}
