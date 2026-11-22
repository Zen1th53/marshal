//go:build linux

package tui

import (
	"slices"
	"strings"
	"testing"
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

// A click on a registry-action region runs exactly what the row's key does:
// the help button opens the help, as ? does.
func TestBoardMouseActionHit(t *testing.T) {
	w, _ := boardWorkspace(t)
	w.boardHits.set([]boardHit{{row: 0, x0: 0, x1: 4, kind: hitAction, id: "help"}})
	if !click(w, 1, 1) {
		t.Fatal("a click on an action region was not taken")
	}
	if !w.boardHelp.Load() {
		t.Fatal("the help action did not open the help")
	}
}

// The palette is drawn over the Board, so it keeps the mouse; the diff viewer
// owns the whole screen and takes it.
func TestBoardMouseModeWithPalette(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	w.palette.Open()
	if !w.boardMouseWanted() {
		t.Fatal("the palette being open turned the Board's mouse off")
	}
	w.palette.Close()
	w.diffViewer.OpenRaw("task changes", "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-old\n+new\n")
	if w.boardMouseWanted() {
		t.Fatal("the diff viewer did not turn the Board's mouse off")
	}
}

// A first click on a palette row only selects it; a click on the already
// selected row runs it exactly as Enter, and the view it opens appears.
func TestPaletteMouseSelectsThenRuns(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	hw, _, _ := testWatcher(t, nil)
	w.hostWatch.Store(hw)
	w.permissions.inbox = nil
	// Empty query: click a right-column core entry ("help") to select, then
	// click again to run it.
	w.palette.Open()
	helpIdx := slices.IndexFunc(w.palette.filtered, func(a PaletteAction) bool { return a.ID == "help" })
	if helpIdx < 0 {
		t.Fatal("the core palette has no help entry")
	}
	hRow, hCol := cellOf(t, w, 120, 40, "help", 0)
	if !click(w, hRow, hCol) {
		t.Fatal("a click on a core palette cell was not taken")
	}
	if w.palette.Selected() != helpIdx {
		t.Fatalf("the first click selected %d, want help %d", w.palette.Selected(), helpIdx)
	}
	if !w.palette.IsOpen() || w.boardHelp.Load() {
		t.Fatal("the first click ran the entry instead of only selecting it")
	}
	if !click(w, hRow, hCol) {
		t.Fatal("the second click on help was not taken")
	}
	if w.palette.IsOpen() || !w.boardHelp.Load() {
		t.Fatal("the second click did not close the palette and open help")
	}
	w.boardHelp.Store(false)

	// Search query: filter for "view" so multiple rows show and Open Host view
	// is not the selected row; first click selects it, second click opens it.
	w.palette.Open()
	for _, r := range "view" {
		w.palette.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	want := slices.IndexFunc(w.palette.filtered, func(a PaletteAction) bool { return a.ID == "view.host" })
	if want < 0 {
		t.Fatal("the palette has no host view row")
	}
	if w.palette.Selected() == want {
		w.palette.Select((want + 1) % len(w.palette.filtered))
	}
	row, col := cellOf(t, w, 120, 40, "Open Host view", 0)
	if !click(w, row, col) {
		t.Fatal("a click on a palette row was not taken")
	}
	if w.palette.Selected() != want {
		t.Fatalf("the first click selected %d, want the host row %d", w.palette.Selected(), want)
	}
	if !w.palette.IsOpen() || w.hostView.Load() {
		t.Fatal("the first click ran the row instead of only selecting it")
	}
	if !click(w, row, col) {
		t.Fatal("the second click was not taken")
	}
	if w.palette.IsOpen() {
		t.Fatal("the second click did not close the palette")
	}
	if !w.hostView.Load() {
		t.Fatal("the second click did not open the host view")
	}
}

// The wheel moves the palette's selection; a click outside the rows (the
// header) closes the palette.
func TestPaletteMouseWheelAndOutside(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	w.palette.Open()
	plainFrame(w, 120, 40) // paint so the rows record regions
	before := w.palette.Selected()
	if !w.handleBoardMouse(nil, KeyEvent{Type: KeyMouse, MouseButton: 65, MouseRow: 12, MouseColumn: 12}) {
		t.Fatal("the wheel event was not taken")
	}
	if w.palette.Selected() != before+1 {
		t.Fatalf("the wheel moved the selection to %d, want %d", w.palette.Selected(), before+1)
	}
	row, col := cellOf(t, w, 120, 40, "ACTIONS", 0)
	if !click(w, row, col) {
		t.Fatal("the header click was not taken")
	}
	if w.palette.IsOpen() {
		t.Fatal("a click on the header did not close the palette")
	}
}

// A help line that names a runnable key is a button: a click closes help and
// opens the view, as pressing the key would.
func TestHelpLinesAreButtons(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	hw, _, _ := testWatcher(t, nil)
	w.hostWatch.Store(hw)
	w.openBoardHelp()
	row, col := cellOf(t, w, 120, 40, "host: temperature, disk, guards", 0)
	if !click(w, row, col) {
		t.Fatal("a click on a help line was not taken")
	}
	if w.boardHelp.Load() {
		t.Fatal("the click did not close help")
	}
	if !w.hostView.Load() {
		t.Fatal("the click did not open the host view")
	}
}

// Every registry action that a key runs (it opens a view, sends a key or runs
// a command) is reachable in the palette by filtering to it, so the hint bar's
// palette plus one row reaches it by mouse. A view row is searched by its view
// name: its palette title is "Open <View> view", not its help What.
func TestEveryBoardActionClickable(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	for _, k := range boardKeymap {
		if k.Press == nil && k.Command == "" && k.View == "" {
			continue
		}
		query := k.What
		if k.View != "" {
			query = k.View
		}
		w.palette.Open()
		for _, r := range query {
			w.palette.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
		}
		found := slices.ContainsFunc(w.palette.filtered, func(a PaletteAction) bool { return a.ID == k.ID })
		w.palette.Close()
		if !found {
			t.Errorf("the palette does not surface %q by %q", k.ID, query)
		}
	}
}
