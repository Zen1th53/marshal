package tui

import (
	"github.com/Zen1th53/marshal/internal/board/vm"
)

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

// setV2CardShown records which v2 screen the last paint drew, so the card
// keys act only while their card is on screen (handleBoardKey).
func (w *Workspace) setV2CardShown(s vm.Screen) {
	c := w.v2CardState()
	c.mu.Lock()
	c.shown = s
	c.mu.Unlock()
	w.noteV2Ready(false, false)
}
