package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// The inspector's action row (mockup 03) shows on a task with a decision
// waiting, and each button is the inspector's key: it takes the decision
// to the inbox and opens the same confirmation, never decides.
func TestBoardInspectorActionRowButtons(t *testing.T) {
	for _, b := range []struct{ label, prompt string }{{"[A] approve-all", "Enter to confirm A"}, {"[a] approve", "Enter to confirm a"}, {"[d] deny", "Enter to confirm d"}, {"[r] rework", "Reason then Enter"}} {
		w, _ := boardWorkspace(t)
		w.boardUI.focus, w.boardUI.task, w.boardUI.inspect = layout.Tasks, "T-023", "T-023"
		got := plainFrame(w, 120, 40)
		if !strings.Contains(got, "[A] approve-all  [a] approve  [d] deny  [r] rework  [x] cancel") {
			t.Fatalf("no action row on the inspector:\n%s", got)
		}
		pending := len(w.boardInbox())
		r, c := cellOf(t, w, 120, 40, b.label, 0)
		if !click(w, r, c) {
			t.Fatalf("%s: click not taken", b.label)
		}
		if w.boardUI.inspect != "" || w.boardUI.focus != layout.Inbox || !w.inboxView.Confirming() || !strings.HasPrefix(w.inboxView.Prompt(), b.prompt) {
			t.Fatalf("%s: inspect %q focus %s confirming %v prompt %q", b.label, w.boardUI.inspect, w.boardUI.focus, w.inboxView.Confirming(), w.inboxView.Prompt())
		}
		if len(w.boardInbox()) != pending {
			t.Fatalf("%s decided the request", b.label)
		}
	}
	// x is the inspector's x: to the inbox, nothing in progress.
	w, _ := boardWorkspace(t)
	w.boardUI.focus, w.boardUI.task, w.boardUI.inspect = layout.Tasks, "T-023", "T-023"
	r, c := cellOf(t, w, 120, 40, "[x] cancel", 0)
	if !click(w, r, c) || w.boardUI.inspect != "" || w.boardUI.focus != layout.Inbox || w.inboxView.Confirming() {
		t.Fatalf("[x]: inspect %q focus %s confirming %v", w.boardUI.inspect, w.boardUI.focus, w.inboxView.Confirming())
	}
}

// A task with nothing waiting has no decision buttons, only [o] when its
// worker has a pane, and the row is not clickable where it is not drawn.
func TestBoardInspectorActionRowOnlyWhatApplies(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.boardUI.focus, w.boardUI.task, w.boardUI.inspect = layout.Tasks, "T-017", "T-017"
	got := plainFrame(w, 120, 40)
	if strings.Contains(got, "approve-all") || !strings.Contains(got, "no pending decision") || !strings.Contains(got, "[o] open pane") {
		t.Fatalf("T-017 action row:\n%s", got)
	}
	oldIn := boardInTmux
	defer func() { boardInTmux = oldIn }()
	boardInTmux = func(*Workspace) bool { return false }
	r, c := cellOf(t, w, 120, 40, "[o] open pane", 0)
	if !click(w, r, c) || w.boardUI.lane != "codex-1" && w.boardUI.lane == "" {
		t.Fatalf("[o] click: lane %q", w.boardUI.lane)
	}
}
