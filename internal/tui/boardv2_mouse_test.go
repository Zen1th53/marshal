//go:build linux

package tui

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// v2Hit paints w at 170x45 with Board v2 and returns the first click region
// of kind (and id, when given).
func v2Hit(t *testing.T, w *Workspace, kind boardHitKind, id string) (boardHit, bool) {
	t.Helper()
	w.frameLines(170, 45)
	w.boardHits.mu.Lock()
	defer w.boardHits.mu.Unlock()
	for _, h := range w.boardHits.hits {
		if h.kind == kind && (id == "" || h.id == id) {
			return h, true
		}
	}
	return boardHit{}, false
}

// v2Click clicks the region's first cell.
func v2Click(t *testing.T, w *Workspace, h boardHit) {
	t.Helper()
	if !w.handleBoardMouse(context.Background(), KeyEvent{Type: KeyMouse, MouseRow: h.row + 1, MouseColumn: h.x0 + 1}) {
		t.Fatalf("the Board did not take a click on %+v", h)
	}
}

// Board v2 records the classic Board's click regions for what it draws, so a
// click acts as it does there: a task row selects and then inspects, a lane
// row focuses the lanes, a hint on the key bar runs its action, a card
// button presses its key (and an inbox decision's button only opens the
// confirmation), and an inbox line selects its request.
func TestBoardV2ClicksActAsOnTheClassicBoard(t *testing.T) {
	t.Setenv("MARSHAL_BOARD", "")

	t.Run("tasks and lanes", func(t *testing.T) {
		w := runHintWorkspace(t, "T-017")
		defer w.Close()
		task, ok := v2Hit(t, w, hitTask, "T-023")
		if !ok {
			t.Fatal("no click region on task T-023")
		}
		v2Click(t, w, task)
		if w.boardUI.task != "T-023" || w.boardUI.inspect != "" {
			t.Fatalf("a first click: task %q, inspect %q", w.boardUI.task, w.boardUI.inspect)
		}
		task, _ = v2Hit(t, w, hitTask, "T-023")
		v2Click(t, w, task)
		if w.boardUI.inspect != "T-023" {
			t.Fatalf("a second click did not inspect T-023: %q", w.boardUI.inspect)
		}
		w.boardUI.closeInspector()
		lane, ok := v2Hit(t, w, hitLane, "")
		if !ok {
			t.Fatal("no click region on a lane")
		}
		v2Click(t, w, lane)
		if w.boardUI.focus != layout.Lanes || w.boardUI.lane != lane.id {
			t.Fatalf("a lane click: focus %q, lane %q, want %q", w.boardUI.focus, w.boardUI.lane, lane.id)
		}
	})

	t.Run("hint bar", func(t *testing.T) {
		w := runHintWorkspace(t, "T-017")
		defer w.Close()
		help, ok := v2Hit(t, w, hitAction, "help")
		if !ok || help.row != 45-2 {
			t.Fatalf("no help button on the key bar: %+v", help)
		}
		v2Click(t, w, help)
		if !w.boardHelp.Load() {
			t.Fatal("a click on ? help did not open help")
		}
	})

	t.Run("decision card and inbox", func(t *testing.T) {
		w, _ := boardWorkspace(t)
		defer w.Close()
		defer v2Cards.Delete(w)
		before := len(w.boardInbox())
		if _, ok := v2Hit(t, w, hitTask, ""); ok {
			t.Fatal("a task under the decision card is clickable")
		}
		allow, ok := v2Hit(t, w, hitV2Key, "a")
		if !ok {
			t.Fatal("no allow button on the decision card")
		}
		v2Click(t, w, allow)
		if !w.inboxView.Confirming() || len(w.boardInbox()) != before {
			t.Fatalf("the allow button did not only open the confirmation (confirming %v, %d of %d left)", w.inboxView.Confirming(), len(w.boardInbox()), before)
		}
		// Esc cancels the confirmation; a second Esc puts the card off.
		w.handleBoardKey(KeyEvent{Type: KeyEsc})
		if w.inboxView.Confirming() {
			w.handleBoardInboxKey(context.Background(), KeyEvent{Type: KeyEsc})
		}
		w.frameLines(170, 45)
		if !w.handleBoardKey(KeyEvent{Type: KeyEsc}) {
			t.Fatal("Esc on the decision card was not taken")
		}
		line, ok := v2Hit(t, w, hitInbox, "")
		if !ok {
			t.Fatal("no click region on an inbox line once the card is put off")
		}
		w.boardUI.focus = layout.Tasks
		v2Click(t, w, line)
		if w.boardUI.focus != layout.Inbox || w.inboxView.Selected() != line.id {
			t.Fatalf("an inbox line click: focus %q, selected %q, want %q", w.boardUI.focus, w.inboxView.Selected(), line.id)
		}
	})

	t.Run("classic keeps its own", func(t *testing.T) {
		t.Setenv("MARSHAL_BOARD", "classic")
		w := runHintWorkspace(t, "T-017")
		defer w.Close()
		if _, ok := v2Hit(t, w, hitV2Key, ""); ok {
			t.Fatal("the classic Board recorded a v2 button")
		}
		if _, ok := v2Hit(t, w, hitTask, "T-023"); !ok {
			t.Fatal("the classic Board lost its task regions")
		}
	})
}
