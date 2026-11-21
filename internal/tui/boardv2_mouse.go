package tui

import (
	"context"
	"strings"
	"unicode/utf8"

	boardv2 "github.com/Zen1th53/marshal/internal/tui/board/v2"
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

// v2BoardHit is the classic region for one v2 region; false for one a
// click does nothing on. hintIDs are the hint bar actions the key bar
// shows, in order; empty means the key bar is a card's own.
func v2BoardHit(b vm.Board, h boardv2.Hit, hintIDs []string, empty bool) (boardHit, bool) {
	out := boardHit{row: h.Row, x0: h.X0, x1: h.X1}
	at := func(n int) bool { return h.Index >= 0 && h.Index < n }
	switch h.Kind {
	case boardv2.HitKey:
		if len(hintIDs) > 0 {
			if !at(len(hintIDs)) {
				return out, false
			}
			out.kind, out.id = hitAction, hintIDs[h.Index]
			return out, true
		}
		out.kind, out.id = hitV2Key, h.Key
	case boardv2.HitAction, boardv2.HitReady:
		// An owner card's chip runs the classic card's click action
		// (boardv2_cards_owner.go); any other button presses its key.
		if d := b.Decision; h.Kind == boardv2.HitAction && d != nil && at(len(d.Refs)) && d.Refs[h.Index] != "" {
			return v2OwnerHit(out, d.Refs[h.Index])
		}
		out.kind, out.id = hitV2Key, h.Key
	case boardv2.HitLine:
		if b.Decision == nil || !at(len(b.Decision.Lines)) {
			return out, false
		}
		return v2OwnerHit(out, b.Decision.Lines[h.Index].Ref)
	case boardv2.HitCheck:
		if !empty || !at(len(b.Ready.Checks)) {
			return out, false
		}
		provider := checkProvider(b.Ready.Checks[h.Index])
		if provider == "" {
			return out, false
		}
		out.kind, out.id = hitVerify, provider
	case boardv2.HitLane:
		if !at(len(b.Lanes.Items)) || b.Lanes.Items[h.Index].Name == "" {
			return out, false
		}
		out.kind, out.id = hitLane, b.Lanes.Items[h.Index].Name
	case boardv2.HitTask:
		if !at(len(b.Tasks)) || b.Tasks[h.Index].ID == "" {
			return out, false
		}
		out.kind, out.id = hitTask, b.Tasks[h.Index].ID
	case boardv2.HitInbox:
		if !at(len(b.Inbox.Refs)) {
			return out, false
		}
		ref := b.Inbox.Refs[h.Index]
		switch {
		case strings.HasPrefix(ref, v2RefInbox):
			out.kind, out.id = hitInbox, strings.TrimPrefix(ref, v2RefInbox)
		case strings.HasPrefix(ref, v2RefGate):
			// The line is the gate's reply button: it arms the reply,
			// which Enter or a second click sends (clickGate).
			out.kind, out.id, out.action = hitGate, strings.TrimPrefix(ref, v2RefGate), 'y'
		default:
			return out, false
		}
	default:
		return out, false
	}
	return out, out.id != ""
}

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

// v2CardKey runs a key on the v2 decision or paused card the last paint
// drew, before the Board's other keys; false when no card is on screen or
// the card does not take the key. Text in the composer keeps its keys, and
// an inbox confirmation keeps them unless the card is that inbox decision.
func (w *Workspace) v2CardKey(event KeyEvent) bool {
	if !boardV2On() || w.composing() || w.v2CardShown() == "" {
		return false
	}
	if w.inboxView.Confirming() {
		if d, ok := w.v2DecisionNow(boardNow()); !ok || d.kind != decisionInbox {
			return false
		}
	}
	return w.handleV2CardKey(event)
}
