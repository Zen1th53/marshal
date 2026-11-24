//go:build linux

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
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

// Typing on the Board opens the message line ("to the Marshal ›") without s;
// Esc closes that line too, dropping what was typed as it does after s.
func TestBoardEscClosesTypedMessage(t *testing.T) {
	w := closedRunBesideChat(t)
	ctx := t.Context()
	w.handleTerminalKey(ctx, KeyEvent{Type: KeyRune, Rune: 'n'})
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
