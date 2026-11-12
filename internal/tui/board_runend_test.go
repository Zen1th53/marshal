package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// closedRunBesideChat is a Board beside the Marshal chat whose watched run
// just closed with one merged task, its "what next?" card open.
func closedRunBesideChat(t *testing.T) *Workspace {
	t.Helper()
	w, _, _ := boardWithNativeChat(t)
	run := marshal.Run{State: marshal.Closed, PlanID: "P", Tasks: []marshal.Task{{PlanTaskID: "calc-add", Worker: "codex", State: marshal.Merged, Files: []string{"calc.py"}}}}
	p := newMarshalPanel("run", "codex", run, "done")
	w.setMarshalPanel(p)
	w.openNext(p.Board.Run)
	w.dock.docked.Store(true)
	return w
}

// Beside the chat the Board is one column; the inbox gave an open card no
// rows when no request waited, so the "what next?" card of a closed run
// showed only its heading ("INBOX this run is done") and none of its keys.
// The card's rows are counted for the layout now, and the header says the
// run is done instead of "waiting for you".
func TestNextCardShowsBesideTheChat(t *testing.T) {
	w := closedRunBesideChat(t)
	if w.boardLayoutMode() != layout.Split {
		t.Fatalf("layout mode %q, want split", w.boardLayoutMode())
	}
	text := frameText(w, 80, 30)
	for _, want := range []string{"done · Done: 1 of 1 tasks verified and merged.", "[n] new goal", "[d] details", "[q] quit"} {
		if !strings.Contains(text, want) {
			t.Errorf("the card beside the chat misses %q:\n%s", want, text)
		}
	}
	head := strings.SplitN(text, "\n", 3)[1]
	if !strings.Contains(head, " done ") || strings.Contains(head, "waiting for you") {
		t.Fatalf("header of a closed run with only the what-next card: %q", head)
	}
}

// Typing on the Board opens the message line ("to the Marshal ›") without s;
// Esc closes that line too, dropping what was typed as it does after s.
func TestBoardEscClosesTypedMessage(t *testing.T) {
	w := closedRunBesideChat(t)
	ctx := t.Context()
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 'i'})
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyEnter})
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 'x'})
	if !w.composing() || !strings.Contains(frameText(w, 80, 30), "to the Marshal › x") {
		t.Fatalf("typing did not open the message line: composing=%v", w.composing())
	}
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyEsc})
	if w.composing() || w.composer.Text() != "" {
		t.Fatalf("Esc left the typed message line open: %q", w.composer.Text())
	}
}

// The header names the close-out phases and treats the "what next?" card of
// a closed run as done, not as a wait.
func TestHeaderStateWordsAtRunEnd(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	card := &nextCard{RunID: "R", Done: 1, Total: 1}
	for _, c := range []struct {
		name string
		v    boardVisual
		want string
	}{
		{"merging", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Merging}}}, "merging"},
		{"verifying", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Verifying}}}, "final check: re-running the checks on the merged result"},
		{"closed-next", boardVisual{theme: th, next: card, board: model.Board{Run: model.RunHead{State: marshal.Closed}}}, "done"},
		{"closed-next-guard", boardVisual{theme: th, next: card, guard: &guardAlert{}, board: model.Board{Run: model.RunHead{State: marshal.Closed}}}, "waiting for you"},
	} {
		if got := StripANSI(c.v.stateChip()); got != c.want {
			t.Errorf("%s: stateChip %q, want %q", c.name, got, c.want)
		}
	}
}

// The long final check after a merge says why the run waits, in the header
// of a wide Board; a narrow one keeps "final check".
func TestHeaderFinalCheckSaysWhy(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, true)
	v := boardVisual{theme: th, now: time.Date(2026, 11, 12, 12, 34, 0, 0, time.UTC), board: model.Board{Run: model.RunHead{State: marshal.Verifying, Title: "divide", Done: 1, Total: 1}}}
	for _, w := range []int{80, 120, 160} {
		head := StripANSI(v.header(w))
		if !strings.Contains(head, "final check") {
			t.Errorf("width %d: the header lost the final check: %q", w, head)
		}
		if w >= 120 && !strings.Contains(head, "final check: re-running the checks on the merged result") {
			t.Errorf("width %d: the header does not say why: %q", w, head)
		}
		if got := VisibleLen(head); got != w {
			t.Errorf("width %d: the header is %d cells", w, got)
		}
	}
}

// Beside the chat the Board's header has no room for the reason: the idle
// lanes say it, so a minutes-long final check is understood.
func TestLanesSayFinalCheckWhenHeaderIsNarrow(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true), now: time.Now(), board: model.Board{Run: model.RunHead{State: marshal.Verifying, Done: 1, Total: 1}}}
	lines := v.lanes(layout.Pane{ID: layout.Lanes, Rect: layout.Rect{W: 80, H: 4}})
	if len(lines) == 0 || !strings.Contains(StripANSI(lines[0]), "final check: re-running the checks on the merged result") {
		t.Fatalf("the lanes do not say why the run waits: %q", lines)
	}
	v.board.Run.State = marshal.Dispatching
	lines = v.lanes(layout.Pane{ID: layout.Lanes, Rect: layout.Rect{W: 80, H: 4}})
	if strings.Contains(StripANSI(lines[0]), "final check") {
		t.Fatalf("the lanes speak of a final check while the run works: %q", lines[0])
	}
}
