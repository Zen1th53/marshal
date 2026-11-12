package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// A run waiting for the operator says what happened and how Enter resolves it.
func TestBoardRunStopNotice(t *testing.T) {
	w, _ := boardWorkspace(t)
	run := fakeRun()
	run.State = marshal.AwaitingUser
	run.Pause = &marshal.Pause{Reason: "verification stopped: exit 1", Resolutions: []string{"Press Enter to continue."}}
	p := newMarshalPanel("R-7f3a", "codex", run, "waiting for you")
	w.setMarshalPanel(p)
	w.permissions.mu.Lock()
	w.permissions.inbox = nil
	w.permissions.mu.Unlock()
	got := plainFrame(w, 170, 45)
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[1], "waiting for you") {
		t.Fatalf("header: %s", lines[1])
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "▮ waits for you · verification stopped: exit 1 · Enter: continue") {
		t.Fatalf("footer: %s", last)
	}
	if !strings.Contains(got, "⚑ run waits for you · verification stopped: exit 1") || !strings.Contains(got, "[Enter] continue") || strings.Contains(got, "/marshal") {
		t.Fatalf("inbox entry:\n%s", got)
	}
	r, c := cellOf(t, w, 170, 45, "[Enter]", 0)
	if !click(w, r, c) || !w.boardUI.stopArmed || w.composer.Text() != "" {
		t.Fatalf("stop click failed to arm action; composer=%q", w.composer.Text())
	}
	w.boardUI.stopArmed = false
	// Enter on the inbox with no request waiting arms the action.
	w.boardUI.focus = layout.Inbox
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) || !w.boardUI.stopArmed || w.composer.Text() != "" {
		t.Fatalf("Enter on the stop did not arm it: composer=%q", w.composer.Text())
	}
}

// A stopped execution (the panel's "stopped: …" note) is a stop too.
func TestBoardRunStoppedNotice(t *testing.T) {
	w, _ := boardWorkspace(t)
	p := newMarshalPanel("R-7f3a", "codex", fakeRun(), "stopped: worker codex exited 2")
	p.Stopped = true
	w.setMarshalPanel(p)
	got := plainFrame(w, 170, 45)
	if !strings.Contains(got, "paused") || !strings.Contains(got, "▮ stopped · The run stopped unexpectedly. · Enter: try again") || strings.Contains(got, "worker codex exited 2") {
		t.Fatalf("stopped notice:\n%s", got)
	}
}

func TestStopEnterUsesStructuredAction(t *testing.T) {
	b := boardmodel.Board{Run: boardmodel.RunHead{State: marshal.AwaitingUser, Next: []string{"Press Enter to try again."}, PauseAction: marshal.PauseRetryTask, PauseTask: "T-1"}}
	stop, ok := (boardVisual{board: b}).stopNotice()
	if !ok || strings.Join(stop.commands, ",") != "/marshal retry T-1,/marshal resume" {
		t.Fatalf("structured action commands: %+v, %v", stop, ok)
	}

	b.Run.PauseAction, b.Run.PauseTask = "", ""
	b.Run.Next = []string{"resolve …, then /marshal retry T-1 and /marshal resume"}
	stop, ok = (boardVisual{board: b}).stopNotice()
	if !ok || strings.Join(stop.commands, ",") != "/marshal retry T-1,/marshal resume" || stop.verb != "retry the task" {
		t.Fatalf("legacy pause commands: %+v, %v", stop, ok)
	}
}

func TestStopEntryHasNoSlashCommands(t *testing.T) {
	for _, action := range []marshal.PauseAction{marshal.PauseResume, marshal.PauseRetryTask, marshal.PauseUsePlan} {
		b := boardmodel.Board{Run: boardmodel.RunHead{State: marshal.AwaitingUser, Paused: "A task is waiting.", Next: []string{"Press Enter to continue."}, PauseAction: action, PauseTask: "T-1"}}
		v := boardVisual{board: b}
		stop, ok := v.stopNotice()
		if !ok {
			t.Fatal("missing stop notice")
		}
		entry := StripANSI(strings.Join(v.stopEntry(stop, 0, 0, 120), "\n"))
		line := StripANSI(v.stopLine(stop))
		for _, got := range []string{entry, line} {
			if strings.Contains(got, "/marshal") {
				t.Fatalf("bad stop text for %s: %q", action, got)
			}
		}
		if !strings.Contains(entry, "[Enter]") || !strings.Contains(entry, stop.verb) || !strings.Contains(line, stop.verb) {
			t.Fatalf("entry misses action %q: %s", stop.verb, entry)
		}
	}
}

func TestStopEnterArmsThenActs(t *testing.T) {
	w, _ := boardWorkspace(t)
	commands := []string{}
	old := boardStopHandle
	boardStopHandle = func(_ *Workspace, _ context.Context, cmd string) (string, error) {
		commands = append(commands, cmd)
		return "ok", nil
	}
	t.Cleanup(func() { boardStopHandle = old })
	run := fakeRun()
	run.State = marshal.AwaitingUser
	run.Pause = &marshal.Pause{Reason: "A task couldn't start.", Resolutions: []string{"Press Enter to try it again."}, Action: marshal.PauseRetryTask, TaskID: "T-1"}
	p := newMarshalPanel("R-7f3a", "codex", run, "waiting for you")
	w.setMarshalPanel(p)
	w.permissions.mu.Lock()
	w.permissions.inbox = nil
	w.permissions.mu.Unlock()
	w.boardUI.focus = layout.Inbox
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) || len(commands) != 0 {
		t.Fatalf("first Enter acted: %v", commands)
	}
	if got := StripANSI(plainFrame(w, 170, 45)); !strings.Contains(got, "Press Enter again to retry the task") {
		t.Fatalf("armed hint missing: %s", got)
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) || strings.Join(commands, ",") != "/marshal retry T-1,/marshal resume" {
		t.Fatalf("commands: %v", commands)
	}
	if w.composer.Text() != "" {
		t.Fatalf("composer was changed: %q", w.composer.Text())
	}
}

func TestStopEnterOtherKeyDisarms(t *testing.T) {
	w, _ := boardWorkspace(t)
	run := fakeRun()
	run.State = marshal.AwaitingUser
	run.Pause = &marshal.Pause{Reason: "wait", Resolutions: []string{"Press Enter to continue."}}
	w.setMarshalPanel(newMarshalPanel("R-7f3a", "codex", run, "waiting for you"))
	w.permissions.mu.Lock()
	w.permissions.inbox = nil
	w.permissions.mu.Unlock()
	w.boardUI.focus = layout.Inbox
	w.handleBoardKey(KeyEvent{Type: KeyEnter})
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'x'}) || w.boardUI.stopArmed {
		t.Fatal("other key did not disarm and consume the stop")
	}
}

func TestStopFocusesInbox(t *testing.T) {
	w, _ := boardWorkspace(t)
	run := fakeRun()
	run.State = marshal.AwaitingUser
	run.Pause = &marshal.Pause{Reason: "wait", Resolutions: []string{"Press Enter to continue."}}
	p := newMarshalPanel("R-7f3a", "codex", run, "waiting for you")
	lines, _, _ := w.boardFrame(UIState{Marshal: p}, w.theme, nil, 170, 45)
	_, stopped := (boardVisual{board: p.Board, note: p.Note, stopped: p.Stopped}).stopNotice()
	if got := w.boardUI.pane(len(w.boardInbox()) > 0 || stopped); got != layout.Inbox || len(lines) == 0 {
		t.Fatalf("stop did not focus inbox: %s", got)
	}
	// With no focus chosen, Enter reaches the stop notice.
	w.setMarshalPanel(p)
	w.permissions.mu.Lock()
	w.permissions.inbox = nil
	w.permissions.mu.Unlock()
	w.boardUI.focus = ""
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) || !w.boardUI.stopArmed {
		t.Fatal("Enter with default focus did not arm the stop")
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
	help, _, _ := v.helpOverlay(160, 80, 0)
	got := StripANSI(strings.Join(help, "\n"))
	for _, want := range []string{"MOUSE", "task  lane", "header chip", "open its confirmation", "[Enter]", "do what it suggests", "footer key", "closes help"} {
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

// The governed build's approval refusal stops execution; the stop keeps its
// approval id, which /marshal approve-task needs. Other errors stay plain.
func TestStoppedApprovalRefusalKeepsApprovalID(t *testing.T) {
	refusal := errors.New("dispatch T-1: process 05: approval appr-1 required; use /marshal approve-task appr-1, then /marshal resume")
	note := marshalOutcomeNote(marshal.Run{}, refusal)
	b := boardmodel.Board{Run: boardmodel.RunHead{State: marshal.Dispatching}}
	stop, ok := (boardVisual{board: b, stopped: true, note: note}).stopNotice()
	if !ok || !strings.HasPrefix(stop.reason, "process 05: approval appr-1 required") {
		t.Fatalf("approval refusal stop: %+v", stop)
	}
	note = marshalOutcomeNote(marshal.Run{}, errors.New("worker: exit 2: /tmp/x"))
	if stop, _ := (boardVisual{board: b, stopped: true, note: note}).stopNotice(); note != "The run stopped unexpectedly." || stop.reason != "The run stopped unexpectedly." {
		t.Fatalf("plain stop: %q %+v", note, stop)
	}
}

// A stop whose error says why in plain words (a delivery the person's
// checkout is in the way of) shows that reason and what to do, not "The run
// stopped unexpectedly.", on the Board's stop card and footer.
func TestBoardStopShowsKnownDeliveryReason(t *testing.T) {
	stop := &app.DeliveryStopError{Reason: "Your checkout of master has uncommitted changes (calc.py, test_calc.py).", Next: "Stash them (git stash -u) or undo them, then press Enter to finish delivering.", Cause: errors.New("raw /tmp/x detail")}
	err := fmt.Errorf("close: %w", stop)
	note := marshalOutcomeNote(marshal.Run{}, err)
	b := boardmodel.Board{Run: boardmodel.RunHead{State: marshal.Verifying}}
	got, ok := (boardVisual{board: b, stopped: true, note: note}).stopNotice()
	if !ok || got.reason != stop.Reason || got.next != stop.Next || strings.Join(got.commands, ",") != "/marshal resume" {
		t.Fatalf("known stop: %+v (note %q)", got, note)
	}
	if strings.Contains(note, "/tmp/x") || strings.Contains(note, "detach") {
		t.Fatalf("note leaks the raw cause: %q", note)
	}
	// The person's own /marshal close: Enter closes again.
	got, ok = (boardVisual{board: b, stopped: true, note: knownCloseStopNote(err)}).stopNotice()
	if !ok || got.reason != stop.Reason || strings.Join(got.commands, ",") != "/marshal close" {
		t.Fatalf("known close stop: %+v", got)
	}

	w, _ := boardWorkspace(t)
	p := newMarshalPanel("R-7f3a", "codex", fakeRun(), note)
	p.Stopped = true
	w.setMarshalPanel(p)
	frame := plainFrame(w, 170, 45)
	if strings.Contains(frame, "stopped unexpectedly") || !strings.Contains(frame, "Your checkout of master has uncommitted changes") {
		t.Fatalf("board frame:\n%s", frame)
	}
}
