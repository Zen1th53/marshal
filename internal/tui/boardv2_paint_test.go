//go:build linux

package tui

import (
	"strings"
	"testing"

	boardv2 "github.com/Zen1th53/marshal/internal/tui/board/v2"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The Board home is drawn by Board v2.
func TestBoardV2IsTheDefaultHome(t *testing.T) {
	w := emptyBoardWorkspace(t)
	lines, row, _ := w.frameLinesFull(132, 31)
	if len(lines) != 31 || row != 31 {
		t.Fatalf("%d lines, cursor row %d", len(lines), row)
	}
	text := StripANSI(strings.Join(lines, "\n"))
	for _, want := range []string{" MARSHAL ", " NOW ", "READINESS", "DEFAULTS"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q on the v2 home:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Tell the Marshal what to build") {
		t.Fatalf("classic first-use card on the v2 home:\n%s", text)
	}
}

// Below 80 columns (or 12 rows) one line says the window is too small and
// nothing else is drawn.
func TestBoardTooSmallIsOneLine(t *testing.T) {
	w := emptyBoardWorkspace(t)
	for _, size := range [][2]int{{79, 24}, {120, 11}, {40, 10}} {
		lines, _, _ := w.frameLinesFull(size[0], size[1])
		if len(lines) != size[1] {
			t.Fatalf("%v: %d lines", size, len(lines))
		}
		text := StripANSI(strings.Join(lines, "\n"))
		if strings.Contains(text, " NOW ") || strings.Contains(text, "INBOX") {
			t.Fatalf("%v: a pane drawn when too small:\n%s", size, text)
		}
		if !strings.Contains(text, "make the window") {
			t.Fatalf("%v: no too-small line:\n%s", size, text)
		}
		var drawn int
		for _, l := range lines {
			if strings.TrimSpace(StripANSI(l)) != "" {
				drawn++
			}
		}
		if drawn != 1 {
			t.Fatalf("%v: %d lines drawn:\n%s", size, drawn, text)
		}
	}
}

// The composer line takes the last row.
func TestBoardV2StatusOnLastRow(t *testing.T) {
	w := emptyBoardWorkspace(t)
	w.composer.SetText("hello")
	lines, _, _ := w.frameLinesFull(120, 30)
	if last := StripANSI(lines[len(lines)-1]); !strings.Contains(last, "hello") {
		t.Fatalf("last row %q", last)
	}
}

func TestV2ScreenChoice(t *testing.T) {
	for _, tc := range []struct {
		b               vm.Board
		empty, planning bool
		want            vm.Screen
	}{
		{vm.Board{}, true, false, vm.ScreenReady},
		{vm.Board{}, true, true, vm.ScreenPlanning},
		{vm.Board{}, false, false, vm.ScreenRunning},
		{vm.Board{Decision: &vm.Decision{}}, false, false, vm.ScreenDecision},
		{vm.Board{Paused: &vm.Paused{}}, false, false, vm.ScreenPaused},
	} {
		if got := v2Screen(tc.b, tc.empty, tc.planning); got != tc.want {
			t.Errorf("%+v: %s, want %s", tc, got, tc.want)
		}
	}
}

func TestV2Theme(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	if got := v2Theme(NewTheme(ThemeDefault, true, false)); got.Color != boardv2.ColorTrue || !got.Unicode {
		t.Fatalf("%+v", got)
	}
	t.Setenv("COLORTERM", "")
	t.Setenv("BOARD_TRUECOLOR", "")
	if got := v2Theme(NewTheme(ThemeDefault, false, false)); got.Color != boardv2.Color256 || got.Unicode {
		t.Fatalf("%+v", got)
	}
	if got := v2Theme(NewTheme(ThemeNoColor, true, false)); got.Color != boardv2.ColorNone {
		t.Fatalf("%+v", got)
	}
}
