package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// A run waiting for the operator says so in the header, gives the reason
// and the exact command in the footer, and leads the inbox with it; its
// command button and Enter on the inbox type the command, never run it.
func TestBoardRunStopNotice(t *testing.T) {
	w, _ := boardWorkspace(t)
	run := fakeRun()
	run.State = marshal.AwaitingUser
	run.Pause = &marshal.Pause{Reason: "verification stopped: exit 1", Resolutions: []string{"resolve the cause, then /marshal resume to rerun verification"}}
	p := newMarshalPanel("R-7f3a", "codex", run, "waiting for you · see the escalated task, then /marshal amend or /marshal resume")
	w.setMarshalPanel(p)
	got := plainFrame(w, 170, 45)
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[0], "waiting for you") {
		t.Fatalf("header: %s", lines[0])
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "run waits for you · type /marshal resume · verification stopped: exit 1") {
		t.Fatalf("footer: %s", last)
	}
	if !strings.Contains(got, "⚑ run waits for you · verification stopped: exit 1") || !strings.Contains(got, "type › /marshal resume") {
		t.Fatalf("inbox entry:\n%s", got)
	}
	r, c := cellOf(t, w, 170, 45, "type › /marshal resume", 0)
	if !click(w, r, c+8) || w.composer.Text() != "/marshal resume" {
		t.Fatalf("command button typed %q", w.composer.Text())
	}
	w.composer.SetText("")
	// Enter on the inbox with no request waiting types it too.
	w.permissions.mu.Lock()
	w.permissions.inbox = nil
	w.permissions.mu.Unlock()
	w.boardUI.focus = layout.Inbox
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) || w.composer.Text() != "/marshal resume" {
		t.Fatalf("Enter on the stop typed %q", w.composer.Text())
	}
}

// A stopped execution (the panel's "stopped: …" note) is a stop too.
func TestBoardRunStoppedNotice(t *testing.T) {
	w, _ := boardWorkspace(t)
	p := newMarshalPanel("R-7f3a", "codex", fakeRun(), "stopped: worker codex exited 2")
	p.Stopped = true
	w.setMarshalPanel(p)
	got := plainFrame(w, 170, 45)
	if !strings.Contains(got, "paused") || !strings.Contains(got, "run stopped · type /marshal resume · worker codex exited 2") {
		t.Fatalf("stopped notice:\n%s", got)
	}
}

func TestStopEnterUsesStructuredAction(t *testing.T) {
	b := boardmodel.Board{Run: boardmodel.RunHead{State: marshal.AwaitingUser, Next: []string{"Press Enter to try again."}, PauseAction: marshal.PauseRetryTask, PauseTask: "T-1"}}
	stop, ok := (boardVisual{board: b}).stopNotice()
	if !ok || strings.Join(stop.commands, ",") != "/marshal retry T-1,/marshal resume" {
		t.Fatalf("structured action commands: %+v, %v", stop, ok)
	}

	b.Run.PauseAction = ""
	b.Run.Next = []string{"resolve …, then /marshal retry T-1 and /marshal resume"}
	stop, ok = (boardVisual{board: b}).stopNotice()
	if !ok || strings.Join(stop.commands, ",") != "/marshal retry,/marshal resume" {
		t.Fatalf("legacy pause commands: %+v, %v", stop, ok)
	}
}

func TestStoppedRunDetectedWithoutNotePrefix(t *testing.T) {
	b := boardmodel.Board{Run: boardmodel.RunHead{State: marshal.Verifying}}
	if _, ok := (boardVisual{board: b, stopped: true, note: "anything"}).stopNotice(); !ok {
		t.Fatal("stopped execution was not detected without the note prefix")
	}
	if _, ok := (boardVisual{board: b, note: "stopped: x"}).stopNotice(); ok {
		t.Fatal("note prefix alone marked execution stopped")
	}
}

// A panel copied with a new note is no longer the stopped execution's panel.
func TestPanelWithNewNoteIsNotStopped(t *testing.T) {
	w, _ := boardWorkspace(t)
	p := newMarshalPanel("R-7f3a", "codex", fakeRun(), "stopped: worker codex exited 2")
	p.Stopped = true
	w.setMarshalPanel(p)
	if w.marshalPanelWithNote("R-7f3a", "codex", "amendment failed: x").Stopped {
		t.Fatal("a later note kept the stopped flag")
	}
}

// Help lists every kind of click target the Board's mouse acts on.
func TestBoardHelpListsMouse(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true)}
	help, _ := v.helpOverlay(160, 80, 0)
	got := StripANSI(strings.Join(help, "\n"))
	for _, want := range []string{"MOUSE", "task  lane", "header chip", "open its confirmation", "type › cmd", "footer key", "closes help"} {
		if !strings.Contains(got, want) {
			t.Errorf("help misses %q:\n%s", want, got)
		}
	}
	if len(boardMouseHelp) < 5 {
		t.Error("a click target is not listed in help")
	}
}

// A Host view scroll key that cannot move says why instead of doing
// nothing (button walk VK-2-j was DEAD at 180 columns).
func TestHostScrollKeysAnswerAtTheEdge(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.boardUI.hostScroll, w.boardUI.hostMax = 0, 0
	w.hostView.Store(true)
	for _, k := range []rune{'j', 'k'} {
		w.setOutput("", false)
		if !w.handleHostKey(KeyEvent{Type: KeyRune, Rune: k}) {
			t.Fatalf("%c not taken", k)
		}
		w.mu.RLock()
		out := w.state.LastOutput
		w.mu.RUnlock()
		if !strings.Contains(out, "nothing to scroll") {
			t.Fatalf("%c at a view that fits: %q", k, out)
		}
	}
	w.boardUI.hostMax = 3
	w.setOutput("", false)
	w.handleHostKey(KeyEvent{Type: KeyRune, Rune: 'j'})
	w.mu.RLock()
	out := w.state.LastOutput
	w.mu.RUnlock()
	if out != "" || w.boardUI.hostScroll != 1 {
		t.Fatalf("j with room: scroll %d, said %q", w.boardUI.hostScroll, out)
	}
}
