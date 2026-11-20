package tui

import (
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardv2 "github.com/Zen1th53/marshal/internal/tui/board/v2"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// boardV2On reports whether the Board home and run views are drawn by the
// Board v2 renderer. MARSHAL_BOARD=classic keeps the classic Board for one
// release.
func boardV2On() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("MARSHAL_BOARD")), "classic")
}

// v2Theme is the renderer's view of the workspace theme: the colour mode the
// terminal takes and whether it draws Unicode.
func v2Theme(th *Theme) boardv2.Theme {
	if th == nil {
		return boardv2.Theme{Color: boardv2.Color256, Unicode: true}
	}
	t := boardv2.Theme{Unicode: th.Unicode}
	switch th.Mode {
	case ThemeNoColor:
		t.Color = boardv2.ColorNone
	case ThemeMonochrome:
		t.Color = boardv2.ColorMono
	case ThemeHighContrast:
		t.Color = boardv2.Color256
	default:
		ct := os.Getenv("COLORTERM")
		if ct == "truecolor" || ct == "24bit" || os.Getenv("BOARD_TRUECOLOR") == "1" {
			t.Color = boardv2.ColorTrue
		} else {
			t.Color = boardv2.Color256
		}
	}
	return t
}

// v2Screen picks the screen from what the workspace knows: a card that
// needs the owner first, then the run, then planning, then readiness.
func v2Screen(b vm.Board, empty, planning bool) vm.Screen {
	switch {
	case b.Decision != nil:
		return vm.ScreenDecision
	case b.Paused != nil:
		return vm.ScreenPaused
	case !empty:
		return vm.ScreenRunning
	case planning:
		return vm.ScreenPlanning
	}
	return vm.ScreenReady
}

// paintV2 draws the Board home or run view with the v2 renderer. In Split
// mode the Marshal chat is a pane of its own beside this one, so only the
// compact Board is drawn. The last row carries the status or composer line.
func (w *Workspace) paintV2(th *Theme, v boardVisual, empty bool, mode layout.Mode, cols, rows int) []string {
	b := w.boardV2()
	// The docked chat's tmux title is the frame's MARSHAL CHAT strip.
	w.noteDockStrip(b)
	b.Screen = v2Screen(b, empty, v.planning || v.goalGiven)
	if b.Now.IsZero() {
		b.Now = v.now
	}
	t := v2Theme(th)
	var lines []string
	if mode == layout.Split {
		lines = boardv2.RenderSide(b, cols, rows, t)
	} else {
		lines = boardv2.Render(b, cols, rows, t)
	}
	if len(lines) > 0 && v.status != "" {
		lines[len(lines)-1] = PadCell(" "+v.status, cols)
	}
	return lines
}

// v2Fits reports whether the v2 screens draw at this size: 80 columns or
// more on its own, 60 or more beside the docked chat. Smaller keeps the
// classic narrow Board.
func (w *Workspace) v2Fits(cols, rows int) bool {
	if !boardV2On() || rows < boardv2.MinRows {
		return false
	}
	if w.boardLayoutMode() == layout.Split {
		return cols >= boardv2.MinCols
	}
	return cols >= layout.MinCols
}

// v2Defers reports whether an owner card is open that the v2 screens do not
// draw yet (an ask, a requirements or change card, an owner gate, a guard
// alert, a stopped run). The classic Board shows it then, so a decision is
// never hidden while the cards collector is not wired.
func v2Defers(v boardVisual) bool {
	return v.ask != nil || v.req != nil || v.change != nil || v.next != nil || v.guard != nil || len(v.gates) > 0 || v.stopped || v.stall != nil
}
