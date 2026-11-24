//go:build linux

package tui

import (
	"strings"
	"testing"
)

// An owner card is decided from focus with the classic card's own keys: Enter
// on the requirements card with blanks answers about the blanks and neither
// closes the card nor opens the chat.
func TestDecisionFocusOwnerCardEnter(t *testing.T) {
	w := reqBoard(t)
	defer w.Close()
	defer v2Cards.Delete(w)
	if !dvKey(w, 'o') || w.boardUI.decisionFocus != focusDecision {
		t.Fatal("o did not focus the requirements card")
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken in focus")
	}
	if w.currentReq() == nil || !strings.Contains(dvOutput(w), "Answer the blank lines first") {
		t.Fatalf("Enter on the requirements: card open=%v answer %q", w.currentReq() != nil, dvOutput(w))
	}
	if w.boardUI.decisionFocus != focusDecision {
		t.Fatal("focus closed although the card did not change")
	}
}
