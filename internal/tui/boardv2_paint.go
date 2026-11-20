package tui

import (
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
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
// The key bar shows the hint bar's keys for the state on screen (board_hints.go)
// unless a card is shown, which keeps its own; the regions the renderer drew
// are recorded so a click acts as on the classic Board (boardv2_mouse.go).
//
// It is called without w.mu held: the collectors read the workspace state
// through its own locked accessors (GetUIState, marshalPanel).
func (w *Workspace) paintV2(th *Theme, v boardVisual, empty bool, mode layout.Mode, cols, rows int) []string {
	b := w.boardV2()
	// The docked chat's tmux title is the frame's MARSHAL CHAT strip.
	w.noteDockStrip(b)
	b.Screen = v2Screen(b, empty, v.planning || v.goalGiven)
	if b.Now.IsZero() {
		b.Now = v.now
	}
	var hintIDs []string
	if b.Decision == nil && b.Paused == nil {
		b.Keys, hintIDs = v.v2HintKeys()
	}
	w.setV2CardShown(b.Screen)
	t := v2Theme(th)
	var lines []string
	var hits []boardv2.Hit
	if mode == layout.Split {
		lines, hits = boardv2.RenderSideHits(b, cols, rows, t)
	} else {
		lines, hits = boardv2.RenderChatHits(b, cols, rows, t, nil)
	}
	if v.status == "" && empty && v.armed != "" {
		// The first-use card says this beside the armed provider; the
		// readiness list has no such line, so the status row says it.
		v.status = "Enter verifies " + v.armed + " (one model call) · any other key cancels"
	}
	last := len(lines) - 1
	status := v.v2StatusRow(b.Decision != nil || b.Paused != nil)
	for _, h := range hits {
		if h.Row == last && status {
			continue
		}
		if bh, ok := v2BoardHit(b, h, hintIDs, empty); ok {
			v.hit(bh)
		}
	}
	if last >= 0 && status {
		// The status row is the classic footer: the status (the composer,
		// a command's answer, an open confirmation's prompt) with the keys
		// of whatever holds the keyboard beside it.
		if v.status == "" {
			v.status = v.inboxPrompt
		}
		line, spans := v.footerSpans(cols)
		lines[last] = line
		for _, sp := range spans {
			v.hit(boardHit{row: last, x0: sp.x0, x1: min(sp.x1, cols), kind: hitAction, id: sp.id})
		}
	}
	return lines
}

// v2StatusRow reports whether the v2 screen's last row carries the classic
// footer: while there is a status, or while a card is shown (its key bar is
// the card's own) and a confirmation, picker or the composer holds the
// keyboard, so its keys stay on screen.
func (v boardVisual) v2StatusRow(card bool) bool {
	if v.status != "" {
		return true
	}
	if !card {
		return false
	}
	if v.inboxPrompt != "" {
		return true
	}
	return len(v.hints) > 0 && hintHoldsKeyboard(v.hints[0])
}

// v2HintKeys is the hint bar's keys as the v2 key bar draws them, with the
// action each stands for.
func (v boardVisual) v2HintKeys() ([]vm.Key, []string) {
	var keys []vm.Key
	var ids []string
	for _, id := range v.hints {
		if k, ok := boardAction(id); ok && k.Hint != "" {
			keys = append(keys, vm.Key{Key: v.hintKey(k.Keys), Label: k.Hint})
			ids = append(ids, id)
		}
	}
	return keys, ids
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
// draw yet, so the classic Board shows it and a decision is never hidden:
// an ask card, the requirements card, a change card, the "what next?" card,
// the guard's alert, and a pause that needs a new plan (the paused card is
// one choice; use_plan is not). Decisions (the architecture and plan gates,
// the inbox) and the other stops are the v2 decision and paused cards.
func v2Defers(v boardVisual) bool {
	if v.ask != nil || v.req != nil || v.change != nil || v.next != nil || v.guard != nil {
		return true
	}
	stop, ok := v.stopNotice()
	return ok && stop.action == marshal.PauseUsePlan
}

// setV2CardShown records which v2 screen the last paint drew, so the card
// keys act only while their card is on screen (handleBoardKey).
func (w *Workspace) setV2CardShown(s vm.Screen) {
	c := w.v2CardState()
	c.mu.Lock()
	c.shown = s
	c.mu.Unlock()
}

// v2CardShown is the card the last paint drew: ScreenDecision,
// ScreenPaused, or "" for none.
func (w *Workspace) v2CardShown() vm.Screen {
	c := w.v2CardState()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shown == vm.ScreenDecision || c.shown == vm.ScreenPaused {
		return c.shown
	}
	return ""
}
