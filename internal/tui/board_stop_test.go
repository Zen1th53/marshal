//go:build linux

package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

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
	for _, want := range []string{"MOUSE", "header chip", "open that view", "footer key", "closes help"} {
		if !strings.Contains(got, want) {
			t.Errorf("help misses %q:\n%s", want, got)
		}
	}
	if len(boardMouseHelp) < 2 {
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

// quotaStop is a stop error the way app's quota wait reports one: a raw
// machine message plus the plain reason and retry time.
type quotaStop struct{}

func (quotaStop) Error() string {
	return "waiting_on_quota: every provider for governed mode is cooling down until 2026-11-19T18:00:00Z"
}
func (quotaStop) StopReason() (string, string) {
	return "Every AI provider for governed mode is at its usage limit until 18:00.", "Press Enter after that time to try again."
}

// A run stopped because every provider is cooling down shows that reason and
// when it can retry on its inbox card, not "The run stopped unexpectedly.".
func TestBoardStopShowsQuotaWaitReason(t *testing.T) {
	note := marshalOutcomeNote(marshal.Run{}, fmt.Errorf("dispatch: %w", quotaStop{}))
	b := boardmodel.Board{Run: boardmodel.RunHead{State: marshal.Verifying}}
	got, ok := (boardVisual{board: b, stopped: true, note: note}).stopNotice()
	if !ok || got.reason != "Every AI provider for governed mode is at its usage limit until 18:00." || got.next != "Press Enter after that time to try again." {
		t.Fatalf("quota stop: %+v (note %q)", got, note)
	}
	w, _ := boardWorkspace(t)
	p := newMarshalPanel("R-7f3a", "codex", fakeRun(), note)
	p.Stopped = true
	w.setMarshalPanel(p)
	frame := plainFrame(w, 170, 45)
	if strings.Contains(frame, "stopped unexpectedly") || !strings.Contains(frame, "usage limit until 18:00") || strings.Contains(frame, "waiting_on_quota") {
		t.Fatalf("board frame:\n%s", frame)
	}
}
