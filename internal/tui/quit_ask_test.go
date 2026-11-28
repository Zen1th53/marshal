package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func quitTestWorkspace(t *testing.T) *Workspace {
	t.Helper()
	t.Setenv("TMUX", "")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "")
	w := NewWorkspace(nil, "demo", "session")
	w.homeBoard = true
	w.theme = NewTheme(ThemeNoColor, true, false)
	w.terminal = NewTerminal(strings.NewReader(""), &strings.Builder{})
	w.screen = NewScreen(w.terminal)
	return w
}

func addQuitTestWorker(w *Workspace) {
	w.tmuxMu.Lock()
	w.tmuxActiveWins["task-a"] = &activeTmuxAgent{id: "task-a", role: "task", paneID: "%8", window: "marshal-task-a"}
	w.tmuxMu.Unlock()
}

// quitFakeTmux is a tmux that lists clients (most recent last in its own
// order) and logs every command.
func quitFakeTmux(t *testing.T, w *Workspace, clients string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$1\" in\n  list-clients) printf '" + clients + "';;\nesac\nexit 0\n"
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "/tmp/tmux-quit-test/default,1,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")
	tmux.SetBinaryPath(path)
	t.Cleanup(tmux.ResetBinaryPath)
	w.tmuxPath = path
	w.tmuxSession = "marshal-quit-test"
	return log
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, _ := os.ReadFile(path)
	return string(data)
}

// With nothing running and no other terminal, q quits at once, as before.
func TestQuitAtOnceWhenNothingRuns(t *testing.T) {
	w := quitTestWorkspace(t)
	if !w.handleTerminalKey(context.Background(), runeKey('q')) {
		t.Fatal("q with nothing running did not quit")
	}
}

// Outside tmux nothing keeps running once Marshal ends, so q while work
// runs asks to stop everything; Esc stays, s stops.
func TestQuitAsksWhileWorkRunsOutsideTmux(t *testing.T) {
	w := quitTestWorkspace(t)
	addQuitTestWorker(w)
	ctx := context.Background()
	if w.handleTerminalKey(ctx, runeKey('q')) {
		t.Fatal("q quit while a worker runs")
	}
	kind, hold := w.quitAsking()
	if kind != quitAskStop || !hold.working {
		t.Fatalf("quit question %v %+v, want stop with work running", kind, hold)
	}
	if s := quitAskStatus(kind, hold); !strings.Contains(s, "s: stop everything") || strings.Contains(s, "detach") {
		t.Fatalf("outside tmux the question offers %q", s)
	}
	if w.handleTerminalKey(ctx, KeyEvent{Type: KeyEsc}) {
		t.Fatal("Esc on the quit question quit")
	}
	if got := quitOutput(w); !strings.Contains(got, "Nothing was stopped") {
		t.Fatalf("Esc answer %q", got)
	}
	if kind, _ := w.quitAsking(); kind != quitAskNone {
		t.Fatal("Esc left the quit question open")
	}
	w.handleTerminalKey(ctx, runeKey('q'))
	if !w.handleTerminalKey(ctx, runeKey('s')) {
		t.Fatal("s on the quit question did not stop everything")
	}
}

// In tmux, q while work runs asks; Enter (the default) detaches only the
// terminal that pressed it, the most recent client, and stops nothing.
func TestQuitDetachIsTheDefaultInTmux(t *testing.T) {
	for _, key := range []KeyEvent{{Type: KeyEnter}, runeKey('d')} {
		w := quitTestWorkspace(t)
		log := quitFakeTmux(t, w, `100\t/dev/ttys001\n`)
		addQuitTestWorker(w)
		ctx := context.Background()
		if w.handleTerminalKey(ctx, runeKey('q')) {
			t.Fatal("q quit while a worker runs")
		}
		kind, hold := w.quitAsking()
		if kind != quitAskDetach {
			t.Fatalf("quit question %v, want detach", kind)
		}
		if s := quitAskStatus(kind, hold); !strings.Contains(s, "⏎ or d: detach (work keeps running)") {
			t.Fatalf("question %q", s)
		}
		if w.handleTerminalKey(ctx, key) {
			t.Fatalf("%v on the quit question quit", key)
		}
		got := readLog(t, log)
		if !strings.Contains(got, "detach-client -t /dev/ttys001") || strings.Contains(got, "kill-") {
			t.Fatalf("tmux calls:\n%s", got)
		}
		if quitOutput(w) != quitDetachedNote {
			t.Fatalf("after detach %q", quitOutput(w))
		}
		if w.commandExitRequested() {
			t.Fatal("detach asked the workspace to exit")
		}
	}
}

// A second terminal on the same session is reason enough to ask, even with
// nothing running: before, q there ended the session under the first
// terminal too. Detach leaves the other terminal attached.
func TestQuitAsksWithAnotherTerminal(t *testing.T) {
	w := quitTestWorkspace(t)
	log := quitFakeTmux(t, w, `100\t/dev/ttys001\n300\t/dev/ttys002\n`)
	ctx := context.Background()
	if w.handleTerminalKey(ctx, runeKey('q')) {
		t.Fatal("q quit with another terminal attached")
	}
	kind, hold := w.quitAsking()
	if kind != quitAskDetach || hold.working || hold.others != 1 {
		t.Fatalf("quit question %v %+v, want detach for one other terminal", kind, hold)
	}
	if s := quitAskStatus(kind, hold); !strings.Contains(s, "Another terminal") || !strings.Contains(s, "closes the other terminal too") {
		t.Fatalf("question %q", s)
	}
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyEnter})
	got := readLog(t, log)
	if !strings.Contains(got, "detach-client -t /dev/ttys002") || strings.Contains(got, "detach-client -t /dev/ttys001") {
		t.Fatalf("detached the wrong terminal:\n%s", got)
	}
}

// Ctrl+C closes the quit question without quitting.
func TestQuitQuestionCtrlCStays(t *testing.T) {
	w := quitTestWorkspace(t)
	addQuitTestWorker(w)
	ctx := context.Background()
	w.handleTerminalKey(ctx, runeKey('q'))
	if w.handleTerminalKey(ctx, KeyEvent{Type: KeyCtrlC}) {
		t.Fatal("Ctrl+C on the quit question quit")
	}
	if kind, _ := w.quitAsking(); kind != quitAskNone {
		t.Fatal("Ctrl+C left the quit question open")
	}
}

func quitOutput(w *Workspace) string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state.LastOutput
}

// A mouse report is not an answer: the quit question stays open.
func TestQuitQuestionIgnoresMouse(t *testing.T) {
	w := quitTestWorkspace(t)
	addQuitTestWorker(w)
	ctx := context.Background()
	w.handleTerminalKey(ctx, runeKey('q'))
	if w.handleTerminalKey(ctx, KeyEvent{Type: KeyMouse}) {
		t.Fatal("a mouse report quit")
	}
	if kind, _ := w.quitAsking(); kind == quitAskNone {
		t.Fatal("a mouse report closed the quit question")
	}
}
