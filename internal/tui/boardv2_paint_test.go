//go:build linux

package tui

import (
	"strings"
	"testing"

	boardv2 "github.com/Zen1th53/marshal/internal/tui/board/v2"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The Board home is drawn by Board v2 by default; MARSHAL_BOARD=classic
// keeps the classic first-use card.
func TestBoardV2IsTheDefaultHome(t *testing.T) {
	t.Setenv("MARSHAL_BOARD", "")
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

	t.Setenv("MARSHAL_BOARD", "classic")
	lines, _, _ = w.frameLinesFull(132, 31)
	if text := StripANSI(strings.Join(lines, "\n")); !strings.Contains(text, "Tell the Marshal what to build") {
		t.Fatalf("MARSHAL_BOARD=classic did not keep the classic Board:\n%s", text)
	}
}

// Below 80 columns the classic narrow Board stays.
func TestBoardV2NarrowKeepsClassic(t *testing.T) {
	t.Setenv("MARSHAL_BOARD", "")
	w := emptyBoardWorkspace(t)
	lines, _, _ := w.frameLinesFull(79, 24)
	if text := StripANSI(strings.Join(lines, "\n")); strings.Contains(text, " NOW ") {
		t.Fatalf("v2 drawn below 80 columns:\n%s", text)
	}
}

// The composer line takes the last row.
func TestBoardV2StatusOnLastRow(t *testing.T) {
	t.Setenv("MARSHAL_BOARD", "")
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
