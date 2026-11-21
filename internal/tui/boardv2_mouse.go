package tui

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The Board v2 screens' clicks. The renderer reports the regions it drew
// (boardv2.Hit); each is recorded as the classic Board's region for the same
// thing, so handleBoardMouse acts on it exactly as on the classic Board: a
// lane or task row selects and then opens, an inbox line selects its request
// or arms its gate's reply, a provider row on the readiness checklist arms
// its verify, the hint bar runs its action, a card button presses the key
// it names, and an owner card's button or line runs the classic card's own
// click action.

// hitV2Key is a button of a v2 card, the readiness checklist or the card's
// own key bar: a click presses the key it names (id), through the same
// handlers the key reaches.
const hitV2Key boardHitKind = "v2key"

// checkProvider is the provider a readiness row is about: the Marshal and
// Workers rows name it first ("claude · Opus 5.5 · signed in", "agy is
// installed but not signed in"); "" for any other row.
func checkProvider(c vm.Check) string {
	if c.Label != "Marshal" && c.Label != "Workers" {
		return ""
	}
	first, _, _ := strings.Cut(strings.TrimSpace(c.Text), " ")
	switch name := providerOf(strings.ToLower(first)); name {
	case "claude", "codex", "agy", "opencode":
		return name
	}
	return ""
}

// v2KeyEvent is the key a v2 button names: "⏎" (or "Enter") is Enter,
// "esc" is Esc, a single character is that rune.
func v2KeyEvent(key string) (KeyEvent, bool) {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "⏎", "enter":
		return KeyEvent{Type: KeyEnter}, true
	case "esc":
		return KeyEvent{Type: KeyEsc}, true
	case "space":
		return KeyEvent{Type: KeyRune, Rune: ' '}, true
	}
	if utf8.RuneCountInString(key) == 1 {
		r, _ := utf8.DecodeRuneInString(key)
		return KeyEvent{Type: KeyRune, Rune: r}, true
	}
	return KeyEvent{}, false
}

// clickV2Key is a click on a v2 button: the key it names, pressed through
// the Board's own handlers in the order the input loop tries them. space
// opens the palette, as the key does.
func (w *Workspace) clickV2Key(ctx context.Context, key string) {
	ev, ok := v2KeyEvent(key)
	if !ok {
		return
	}
	if ev.Type == KeyRune && ev.Rune == ' ' {
		w.closeCompletion()
		w.palette.Toggle()
		w.syncMouseMode()
		return
	}
	if ev.Type == KeyRune && ev.Rune == '?' {
		w.boardHelp.Store(true)
		return
	}
	w.pressBoardKey(ctx, ev)
}
