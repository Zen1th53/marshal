package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// cellOf finds text on the painted frame and returns its first cell, one
// indexed as the terminal reports a click. after skips matches left of it.
func cellOf(t *testing.T, w *Workspace, cols, rows int, text string, after int) (int, int) {
	t.Helper()
	lines, _, _ := w.frameLines(cols, rows)
	for r, line := range lines {
		plain := StripANSI(line)
		from := 0
		for {
			i := strings.Index(plain[from:], text)
			if i < 0 {
				break
			}
			col := VisibleLen(plain[:from+i])
			if col >= after {
				return r + 1, col + 1
			}
			from += i + len(text)
		}
	}
	t.Fatalf("%q not on the %dx%d frame:\n%s", text, cols, rows, plainFrame(w, cols, rows))
	return 0, 0
}

func click(w *Workspace, row, col int) bool {
	return w.handleBoardMouse(nil, KeyEvent{Type: KeyMouse, MouseButton: 0, MouseRow: row, MouseColumn: col})
}

func TestBoardMouseSelectsAndInspectsTasks(t *testing.T) {
	w, _ := boardWorkspace(t)
	row, col := cellOf(t, w, 120, 40, "T-017  ⣾", 0)
	if !click(w, row, col) {
		t.Fatal("a click on a task row was not taken")
	}
	if w.boardUI.focus != layout.Tasks || w.boardUI.task != "T-017" || w.boardUI.inspect != "" {
		t.Fatalf("first click: focus %s task %s inspect %q", w.boardUI.focus, w.boardUI.task, w.boardUI.inspect)
	}
	// Anywhere on the row selects it, not only the id.
	row2, _ := cellOf(t, w, 120, 40, "T-018  ⣾", 0)
	if !click(w, row2, col+30) || w.boardUI.task != "T-018" {
		t.Fatalf("a click on the row's scope did not select it: %s", w.boardUI.task)
	}
	// A click on the selected task inspects it, as Enter does.
	click(w, row2, col)
	if w.boardUI.inspect != "T-018" {
		t.Fatalf("second click did not inspect: %q", w.boardUI.inspect)
	}
	if got := plainFrame(w, 120, 40); !strings.Contains(got, "IDENTITY") {
		t.Fatalf("inspector not painted:\n%s", got)
	}
	// The inspector covers the body: a click under it reaches no task.
	click(w, row, col)
	if w.boardUI.task != "T-018" || w.boardUI.inspect != "T-018" {
		t.Fatalf("a click under the inspector moved the selection to %s", w.boardUI.task)
	}
}

func TestBoardMouseFocusesLanes(t *testing.T) {
	w, _ := boardWorkspace(t)
	oldIn := boardInTmux
	defer func() { boardInTmux = oldIn }()
	boardInTmux = func(*Workspace) bool { return false }
	row, col := cellOf(t, w, 120, 40, "claude-1", 0)
	if !click(w, row, col) || w.boardUI.focus != layout.Lanes || w.boardUI.lane != "claude-1" {
		t.Fatalf("lane click: focus %s lane %s", w.boardUI.focus, w.boardUI.lane)
	}
	if w.boardUI.inspect != "" {
		t.Fatal("the first lane click did more than focus")
	}
	// Again on the focused lane: Enter's action, here the lane's task.
	click(w, row, col)
	if w.boardUI.inspect != "T-018" {
		t.Fatalf("second lane click: inspect %q", w.boardUI.inspect)
	}
}

func TestBoardMouseHeaderChipsOpenTheirView(t *testing.T) {
	w, _ := boardWorkspace(t)
	for _, c := range []struct {
		chip string
		want layout.PaneID
	}{{"inbox ", layout.Inbox}, {"done", layout.Tasks}} {
		row, col := cellOf(t, w, 170, 45, c.chip, 0)
		if row != 2 {
			t.Fatalf("%q found on row %d, not the header", c.chip, row)
		}
		w.boardUI.focus = ""
		if !click(w, row, col) || w.boardUI.focus != c.want {
			t.Fatalf("chip %q: focus %q, want %q", c.chip, w.boardUI.focus, c.want)
		}
	}
	// The running count sits on the vitals row (frame row 0); a click on it
	// focuses the Lanes pane.
	w.boardUI.focus = ""
	if row, col := cellOf(t, w, 170, 45, "running", 0); row != 1 {
		t.Fatalf("running found on row %d, not the vitals row", row)
	} else if !click(w, row, col) || w.boardUI.focus != layout.Lanes {
		t.Fatalf("running click: focus %q", w.boardUI.focus)
	}
	if w.archSrc() == nil {
		t.Fatal("the fixture Board has no Architecture view to open")
	}
	{
		// From a body view (opened by key, no header chip), the inbox chip
		// returns to the Board on the inbox.
		w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'b'})
		if !w.archView.Load() {
			t.Fatal("key b did not open the Architecture view")
		}
		row, col := cellOf(t, w, 170, 45, "inbox ", 0)
		click(w, row, col)
		if w.archView.Load() || w.boardUI.focus != layout.Inbox {
			t.Fatalf("the inbox chip left the view open (%v) or focus %s", w.archView.Load(), w.boardUI.focus)
		}
	}
	// The budget and the clock are read-only: no region, no change.
	row, col := cellOf(t, w, 170, 45, "budget", 0)
	w.boardUI.focus = layout.Lanes
	click(w, row, col)
	if w.boardUI.focus != layout.Lanes {
		t.Fatal("a click on the budget changed the Board")
	}
}

func TestBoardMouseInboxButtonsOpenTheConfirmationOnly(t *testing.T) {
	w, _ := boardWorkspace(t)
	pending := func() int { w.permissions.mu.Lock(); defer w.permissions.mu.Unlock(); return len(w.permissions.inbox) }
	row, col := cellOf(t, w, 120, 40, "[d] deny", 0)
	if !click(w, row, col+1) {
		t.Fatal("a click on deny was not taken")
	}
	if !w.inboxView.Confirming() || !strings.HasPrefix(w.inboxView.Prompt(), "Enter to confirm d") {
		t.Fatalf("deny click: confirming %v prompt %q", w.inboxView.Confirming(), w.inboxView.Prompt())
	}
	if pending() != 1 {
		t.Fatal("a click decided the request")
	}
	// While the confirmation is open the mouse cannot change it.
	w.frameLines(120, 40)
	if r, c := cellOf(t, w, 120, 40, "Enter to confirm d", 0); !click(w, r, c) || !strings.HasPrefix(w.inboxView.Prompt(), "Enter to confirm d") {
		t.Fatalf("a click during the confirmation changed it: %q", w.inboxView.Prompt())
	}
	w.inboxView.Cancel()
	for _, b := range []struct {
		label  string
		action string
	}{{"[A] allow for run", "A"}, {"[a] allow once", "a"}} {
		row, col := cellOf(t, w, 120, 40, b.label, 0)
		click(w, row, col+5)
		if !w.inboxView.Confirming() || w.inboxView.Prompt() != "Enter to confirm "+b.action {
			t.Fatalf("%s: prompt %q", b.label, w.inboxView.Prompt())
		}
		w.inboxView.Cancel()
	}
	row, col = cellOf(t, w, 120, 40, "[i] inspect", 0)
	click(w, row, col)
	if !w.inboxView.Inspecting() || w.inboxView.Confirming() {
		t.Fatal("inspect click did not open the reason")
	}
	if pending() != 1 {
		t.Fatal("a click decided the request")
	}
}

func TestBoardMouseTypedGateNeedsTheID(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.permissions.mu.Lock()
	w.permissions.inbox = []permission.Request{{Kind: "unheard-of", TaskID: "T-023", Object: "release push", Scope: "one run", Reason: "push to main", Who: "Marshal"}}
	w.permissions.mu.Unlock()
	row, col := cellOf(t, w, 120, 40, "confirm ›", 0)
	click(w, row, col)
	if !w.inboxView.Confirming() || !strings.HasPrefix(w.inboxView.Prompt(), "Type ") {
		t.Fatalf("typed gate click: %q", w.inboxView.Prompt())
	}
	if _, ok := w.inboxView.Confirm(); ok {
		t.Fatal("a typed gate confirmed without its id")
	}
}

func TestBoardMouseIgnoresOtherButtonsAndOwnsTheMouseOnlyOnTheBoard(t *testing.T) {
	w, _ := boardWorkspace(t)
	row, col := cellOf(t, w, 120, 40, "T-017  ⣾", 0)
	for _, ev := range []KeyEvent{
		{Type: KeyMouse, MouseButton: 0, MouseRow: row, MouseColumn: col, MouseRelease: true},
		{Type: KeyMouse, MouseButton: 2, MouseRow: row, MouseColumn: col},
		{Type: KeyMouse, MouseButton: 65, MouseRow: row, MouseColumn: col},
	} {
		if !w.handleBoardMouse(nil, ev) || w.boardUI.task == "T-017" {
			t.Fatalf("%+v acted on the task", ev)
		}
	}
	if !w.boardMouseWanted() {
		t.Fatal("the Board does not ask for the mouse")
	}
	w.setBoardView(true)
	if w.boardMouseWanted() || w.handleBoardMouse(nil, KeyEvent{Type: KeyMouse, MouseRow: row, MouseColumn: col}) {
		t.Fatal("the classic screen still takes the mouse for the Board")
	}
}

// The bytes a terminal sends for a click reach the Board through the same
// parser and key loop the workspace runs.
func TestBoardMouseThroughTheKeyLoop(t *testing.T) {
	w, _ := boardWorkspace(t)
	row, col := cellOf(t, w, 120, 40, "T-018  ⣾", 0)
	ev := ParseKey([]byte(fmt.Sprintf("\x1b[<0;%d;%dM", col, row)))
	if ev.Type != KeyMouse {
		t.Fatalf("SGR press parsed as %+v", ev)
	}
	if w.handleTerminalKey(t.Context(), ev) {
		t.Fatal("a click asked the workspace to exit")
	}
	if w.boardUI.task != "T-018" || w.boardUI.focus != layout.Tasks {
		t.Fatalf("the click did not select T-018: %s %s", w.boardUI.task, w.boardUI.focus)
	}
	if w.composer.Text() != "" {
		t.Fatalf("mouse bytes reached the composer: %q", w.composer.Text())
	}
}

// A row the pane gives to "… N more" is not a lane: no region stays on it.
func TestBoardLaneMoreRowIsNotClickable(t *testing.T) {
	b := demoBoard(time.Now())
	var hits []boardHit
	v := boardVisual{board: b, now: time.Now(), theme: NewTheme(ThemeDefault, true, true), hits: &hits}
	p := layout.Pane{ID: layout.Lanes, Rect: layout.Rect{X: 3, Y: 10, W: 90, H: 5}, Density: layout.Full}
	lines := v.lanes(p)
	if last := StripANSI(lines[len(lines)-1]); !strings.HasPrefix(last, "… ") {
		t.Fatalf("no more row: %q", last)
	}
	rows := map[int]string{}
	for _, h := range hits {
		rows[h.row] = h.id
		if h.x0 != 3 || h.x1 != 93 {
			t.Fatalf("lane region %+v is not the pane's width", h)
		}
	}
	if _, ok := rows[10+len(lines)-1]; ok {
		t.Fatalf("the more row is clickable: %v", rows)
	}
	if len(rows) != len(lines)-3 {
		t.Fatalf("%d lane regions for %d lane rows", len(rows), len(lines)-3)
	}
}

// TestBoardMouseFooterHintsPressTheirKey: the footer's key hints are
// buttons, as in the prototype: a click presses the key the hint names.
func TestBoardMouseFooterHintsPressTheirKey(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.boardUI.focus = layout.Tasks
	row, col := cellOf(t, w, 170, 45, "l lanes", 0)
	if !click(w, row, col) || w.boardUI.pane(true) != layout.Lanes {
		t.Fatalf("click on 'l lanes': focus %s", w.boardUI.pane(true))
	}
	row, col = cellOf(t, w, 170, 45, "? help", 0)
	if !click(w, row, col) || !w.boardHelp.Load() {
		t.Fatal("click on '? help' did not open the help")
	}
	w.boardHelp.Store(false)
	row, col = cellOf(t, w, 170, 45, "j/k move", 0)
	focus := w.boardUI.pane(true)
	if !click(w, row, col) || w.boardUI.pane(true) != focus || w.composer.Text() != "" {
		t.Fatal("click on 'j/k move' did something")
	}
}
