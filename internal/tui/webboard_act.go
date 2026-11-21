package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// The Web Board's owner actions (phase 2, docs/design/web-board.md). The
// server (internal/webboard) checks the bearer token, the page's origin and
// the action token; this side presses the button on the Board's own input
// loop, through the handler the terminal key (or the click on that button)
// reaches: handleV2CardKey for a card's key, the owner card's own click
// handler for an owner card's button, sayToMarshal for a message. No command
// is built here, so the web can do only what the card on screen offers.
//
// Authority: an action is the owner's because only the owner holds the
// link, which goes to the footer only (never to Activity, the Marshal chat,
// MCP or a file the Marshal reads). Nothing here accepts an architecture or
// answers for the Marshal: the architecture's Accept stays in its own
// review page, and a self-approval stays refused as in the terminal.

// webActWait is how long an action waits for the input loop; a test seam.
var webActWait = 10 * time.Second

// webBoardAct is the server's Sink: it runs a on the input loop and waits
// for the answer. An action the loop did not reach in time never runs
// later: whichever side claims it first decides.
func (w *Workspace) webBoardAct(ctx context.Context, a webboard.Action) webboard.Result {
	var claimed atomic.Bool
	done := make(chan webboard.Result, 1)
	run := func() {
		if !claimed.CompareAndSwap(false, true) {
			return
		}
		done <- w.webAct(a)
	}
	wait, cancel := context.WithTimeout(ctx, webActWait)
	defer cancel()
	if !w.postUI(wait, run) {
		if claimed.CompareAndSwap(false, true) {
			return webboard.Result{Status: "refused", Text: "The Board is busy; nothing was done. Try again."}
		}
	}
	select {
	case r := <-done:
		return r
	case <-wait.Done():
		if claimed.CompareAndSwap(false, true) {
			return webboard.Result{Status: "refused", Text: "The Board is busy; nothing was done. Try again."}
		}
		return <-done
	}
}

// webAct is one action on the input loop: the page must have acted on the
// card the Board shows now.
func (w *Workspace) webAct(a webboard.Action) webboard.Result {
	b := w.webBoardFrame()
	f := webboard.FromBoard(b)
	if webboard.CardID(f) != a.Card {
		return webboard.Result{Status: "stale", Text: "The Board changed since this page showed it; look again."}
	}
	switch a.Kind {
	case "say":
		return w.webSay(a.Text)
	case "key":
	default:
		return refused("Unknown action.")
	}
	if !slices.Contains(webboard.ActionKeys(f), a.Key) {
		return refused("That button is not on the Board now.")
	}
	label, title := webActionLabel(f, a.Key), webCardTitle(f)
	switch {
	case a.Key == "q":
		return refused("Quit only from the terminal Board.")
	case f.Paused != nil && a.Key == "x" && !a.Confirmed:
		return refused("Press Stop the run again to confirm.")
	}
	before := w.outputSeq.Load()
	took := false
	if d := b.Decision; d != nil && ownerCardKind(d.Kind) {
		// An owner card's button runs the classic card's click action,
		// as a click on it in the terminal does (v2BoardHit).
		if i := slices.IndexFunc(d.Actions, func(k vm.Key) bool { return k.Key == a.Key }); i >= 0 && i < len(d.Refs) && d.Refs[i] != "" {
			took = w.clickOwnerRef(d.Refs[i])
		}
	}
	if !took {
		ev, ok := v2KeyEvent(a.Key)
		if !ok {
			return refused("That button works only in the terminal Board.")
		}
		if a.Text != "" {
			// A hard gate's confirmation reads the canonical id: typed
			// into the confirmation as the terminal does, then Enter.
			if !w.inboxView.Confirming() || !w.inboxView.WantsText() || ev.Type != KeyEnter {
				return refused("Nothing on the Board asks for typed text now.")
			}
			for _, r := range a.Text {
				w.handleV2CardKey(KeyEvent{Type: KeyRune, Rune: r})
			}
		}
		took = w.handleV2CardKey(ev)
	}
	if !took {
		return refused("The Board did not take that button now (is a view open over it?). Nothing was done.")
	}
	res := webboard.Result{Status: "done"}
	if w.outputSeq.Load() != before {
		w.mu.RLock()
		res.Text = w.answer
		w.mu.RUnlock()
	}
	switch {
	case w.inboxView.Confirming():
		res.Confirm = &webboard.Confirm{Label: "Confirm " + label, Prompt: res.Text, Text: w.inboxView.WantsText()}
	case w.v2PlanArmed() != "":
		res.Confirm = &webboard.Confirm{Label: "Send the reply", Prompt: res.Text}
	}
	if f.Decision != nil && f.Decision.Kind == decisionArchitecture && a.Key == "a" {
		// The review's private link goes to this answer only: the page
		// shows it to the owner who asked, never in a frame or Activity.
		res.Link = w.architectureLink()
	}
	line := "From the web: " + label
	if title != "" {
		line += " (" + title + ")"
	}
	w.RecordActivity(line + ".")
	return res
}

func refused(text string) webboard.Result {
	return webboard.Result{Status: "refused", Text: text}
}

// webActionLabel is the button's label on the card.
func webActionLabel(f webboard.Frame, key string) string {
	var ks []webboard.Key
	switch {
	case f.Decision != nil:
		ks = f.Decision.Actions
	case f.Paused != nil:
		ks = f.Paused.Actions
	}
	for _, k := range ks {
		if k.Key == key {
			return k.Label
		}
	}
	if key == "esc" {
		return "later"
	}
	return key
}

func webCardTitle(f webboard.Frame) string {
	switch {
	case f.Decision != nil:
		return f.Decision.Title
	case f.Paused != nil:
		return f.Paused.Title
	}
	return ""
}

// clickOwnerRef runs an owner card button's click action, the handler a
// click on that button reaches in the terminal (handleBoardMouse).
func (w *Workspace) clickOwnerRef(ref string) bool {
	h, ok := v2OwnerHit(boardHit{}, ref)
	if !ok {
		return false
	}
	switch h.kind {
	case hitGuard:
		w.clickGuard(h.action)
	case hitAsk:
		i, err := strconv.Atoi(h.id)
		if err != nil {
			return false
		}
		w.answerAsk(i)
	case hitRequirements:
		w.clickRequirements(h.id)
	case hitChange:
		w.clickChange(h.id)
	case hitNext:
		if h.id == "q" {
			return false
		}
		w.clickNext(h.id)
	default:
		return false
	}
	return true
}

// webSay is "Tell the Marshal…": the message goes the way the Board's s
// composer sends it (sayToMarshal), as the owner's words, never as a
// command, even when it starts with "/".
func (w *Workspace) webSay(text string) webboard.Result {
	text = strings.TrimSpace(text)
	if text == "" {
		return refused("Write something to send.")
	}
	if utf8.RuneCountInString(text) > webboard.SayMax {
		return refused("The message is too long: at most " + strconv.Itoa(webboard.SayMax) + " characters.")
	}
	w.chat.mu.Lock()
	native := w.chat.tr != nil
	w.chat.mu.Unlock()
	if !native && w.sayPane() == "" {
		// sayAs would put the text back into the terminal's composer;
		// from the web nothing is sent and the page keeps the text.
		return refused("No Marshal chat is open, so nothing was sent. /marshal chat in the terminal starts one.")
	}
	w.sayToMarshal(w.workers.context(), text)
	res := webboard.Result{Status: "sent", Text: "Sent to the Marshal."}
	if echo := w.sayEchoList(); len(echo) > 0 {
		switch echo[0].State {
		case sayQueued, saySending:
			if !native {
				res = webboard.Result{Status: "queued", Text: "Queued: the Marshal's chat gets it as soon as it is free; Activity shows when it is in."}
			}
		case sayFailed:
			res = refused("Not sent: " + echo[0].Why + ".")
		}
	}
	w.RecordActivity("From the web: a message to the Marshal (" + res.Status + ").")
	return res
}
