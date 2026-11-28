package tui

import (
	"context"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// The Web Board's owner actions (phase 2). The
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

// webActWait is how long an action waits for the input loop, and
// webCommandWait how long it then waits for a command the key started (the
// paused card's retry, reassign and resume run off the loop); test seams.
var (
	webActWait     = 10 * time.Second
	webCommandWait = 15 * time.Second
)

// webActed is what an action did on the loop: the result, and whether the
// key started a command off the loop that the page should wait for.
type webActed struct {
	res     webboard.Result
	started bool
	// output is set when the key started a command whose answer comes
	// later on the loop (Ctrl+X's /stop all runs on the priority lane).
	output bool
	seq    uint64 // the output sequence after the key
}

// webBoardAct is the server's Sink: it runs a on the input loop and waits
// for the answer. An action the loop did not reach in time never runs
// later: whichever side claims it first decides.
func (w *Workspace) webBoardAct(ctx context.Context, a webboard.Action) webboard.Result {
	var claimed atomic.Bool
	done := make(chan webActed, 1)
	run := func() {
		if !claimed.CompareAndSwap(false, true) {
			return
		}
		if a.Kind == "cmd" && a.Command != nil && webWorkOp(a.Command.Op) {
			go func() { done <- webActed{res: w.webWorkCommand(a)} }()
			return
		}
		if a.Kind == "cmd" && a.Command != nil && webboard.IsTeamOp(a.Command.Op) {
			go func() { done <- webActed{res: w.webTeamCommand(a)} }()
			return
		}
		done <- w.webAct(a)
	}
	wait, cancel := context.WithTimeout(ctx, webActWait)
	defer cancel()
	if !w.postUI(wait, run) {
		if claimed.CompareAndSwap(false, true) {
			return w.webBusyResult("The Board is busy; nothing was done. Try again.")
		}
	}
	var r webActed
	select {
	case r = <-done:
	case <-wait.Done():
		if claimed.CompareAndSwap(false, true) {
			return w.webBusyResult("The Board is busy; nothing was done. Try again.")
		}
		r = <-done
	}
	if !r.started {
		return r.res
	}
	return w.webCommandResult(ctx, r)
}

func (w *Workspace) webBusyResult(text string) webboard.Result {
	receipt := marshal.RefusalReceipt{
		Code:         marshal.RefusalCodeCommandBusy,
		Blocker:      w.marshalSession().currentBlocker(),
		Effects:      marshal.EffectsNone,
		NextCommands: []string{"/marshal status"},
		Message:      text,
	}
	return webboard.Result{Status: "refused", Text: text}.WithRefusal(receipt)
}

// webCommandResult waits, off the loop, for the command a key started and
// answers with its result: "done" with the Board's answer, "refused" when
// it failed, or "started" when it is still running.
func (w *Workspace) webCommandResult(ctx context.Context, r webActed) webboard.Result {
	wait, cancel := context.WithTimeout(ctx, webCommandWait)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for w.commandBusy.Load() || (r.output && w.outputSeq.Load() == r.seq) {
		select {
		case <-wait.Done():
			return webboard.Result{Status: "started", Text: "Started: the Board is running it now; Activity shows how it went."}
		case <-tick.C:
		}
	}
	res := webboard.Result{Status: "done", Text: r.res.Text}
	if w.outputSeq.Load() != r.seq {
		w.mu.RLock()
		text, isErr := w.answer, w.answerIsError
		refusal := w.lastRefusal
		w.mu.RUnlock()
		res.Text = text
		if isErr {
			res.Status = "refused"
		}
		if refusal != nil {
			res = res.WithRefusal(refusal.Clone())
		}
	}
	return res
}

// webAct is one action on the input loop: the page must have acted on the
// card the Board shows now.
func (w *Workspace) webAct(a webboard.Action) webActed {
	if a.Kind == "cmd" {
		if a.Command != nil && webWorkOp(a.Command.Op) {
			return webActed{res: w.webWorkCommand(a)}
		}
		if a.Command != nil && webboard.IsTeamOp(a.Command.Op) {
			return webActed{res: w.webTeamCommand(a)}
		}
		return webActed{res: w.webSetting(a)}
	}
	res, started, seq := w.webActNow(a)
	return webActed{res: res, started: started, seq: seq}
}

// webActNow runs a on the loop; started is set when the key started a
// command off the loop, seq is the output sequence after the key.
func (w *Workspace) webActNow(a webboard.Action) (_ webboard.Result, started bool, seq uint64) {
	b := w.webBoardFrame()
	f := webboard.FromBoard(b)
	if webboard.CardID(f) != a.Card {
		return webboard.Result{Status: "stale", Text: "The Board changed since this page showed it; look again."}, false, 0
	}
	switch a.Kind {
	case "say":
		return w.webSay(a.Text), false, 0
	case "decide":
		if f.Paused != nil || a.Choice == "card/dismiss" {
			return w.webCardAction(f, a)
		}
		return w.webDecide(f, a), false, 0
	default:
		return refused("Unknown action."), false, 0
	}
}

func refused(text string) webboard.Result {
	return webboard.Result{Status: "refused", Text: text}
}

func refusedErr(err error) webboard.Result {
	if receipt, ok := marshal.RefusalOf(err); ok {
		return webboard.Result{Status: "refused", Text: RedactContent(receipt.Summary(), nil)}.WithRefusal(receipt)
	}
	return refused(RedactContent(err.Error(), nil))
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

// Screen shortcuts name typed controls; the page also requires the matching
// capability before offering either button.
const (
	webKeyRetry   = "r"
	webKeyStopAll = "ctrl+x"
)

// webScreenKeys are the screen's own buttons for the page, when no card is
// shown: Retry <task> while r has a stuck task to retry (as the key bar
// offers r), and Stop all workers while a run is on the Board.
func (w *Workspace) webScreenKeys() []vm.Key {
	var keys []vm.Key
	if id := w.webRetryShown(); id != "" && w.runtime != nil {
		keys = append(keys, vm.Key{Key: webKeyRetry, Label: "retry " + cleanBoard(id)})
	}
	if p := w.marshalPanel(); p != nil && w.runtime != nil && len(w.webRunLanes(p.RunID)) > 0 {
		// With a runtime the page sends the bound stop-workers request, so
		// the key is offered only while the run has workers to bind.
		keys = append(keys, vm.Key{Key: webKeyStopAll, Label: "stop all workers"})
	}
	return keys
}

// noteWebRetry records, on the input loop, the task r would retry now, so
// the page's frame (built off the loop) can offer it without reading the
// Board's keyboard state from another goroutine. The paint calls it.
func (w *Workspace) noteWebRetry() {
	s, ok := webBoards.Load(w)
	if !ok {
		return
	}
	id, why := retryTarget(w)
	if why != "" {
		id = ""
	}
	st := s.(*webBoardState)
	st.mu.Lock()
	st.retry = id
	st.mu.Unlock()
}

func (w *Workspace) webRetryShown() string {
	s, ok := webBoards.Load(w)
	if !ok {
		return ""
	}
	st := s.(*webBoardState)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.retry
}

// webCardAction admits a named action against the exact card shown.
func (w *Workspace) webCardAction(f webboard.Frame, a webboard.Action) (webboard.Result, bool, uint64) {
	if a.Revision != webboard.CardID(f) {
		return staleCard("The card changed. Refresh the page and try again."), false, 0
	}
	if a.Value != "" || a.Text != "" || a.Reason != "" {
		return refused("This action does not take text."), false, 0
	}
	if a.Choice == "card/dismiss" {
		if f.Decision == nil {
			return refused("No decision is open."), false, 0
		}
		if w.inboxView.Confirming() {
			w.inboxView.Cancel()
			w.requestRepaint()
		} else if w.v2PlanArmed() != "" {
			s := w.v2CardState()
			s.mu.Lock()
			s.planArmed = ""
			s.mu.Unlock()
			w.memoryAnswer("Reply not sent.", false)
			return webboard.Result{Status: "done", Text: "Reply not sent."}, false, 0
		} else if d, ok := w.v2DecisionNow(boardNow()); ok {
			w.dismissV2Card(d.key)
		}
		return webboard.Result{Status: "done", Text: "Decision set aside. It remains in the inbox."}, false, 0
	}
	if f.Paused == nil {
		return refused("No paused card is open."), false, 0
	}
	index := slices.IndexFunc(f.Paused.Actions, func(k webboard.Key) bool { return webboard.PauseOp(k.Key) == a.Choice })
	if index < 0 {
		return refused("That action is not on the card. Refresh the page."), false, 0
	}
	if a.Choice == "pause/stop" && !a.Confirmed {
		return refused("Click Stop the run again to confirm."), false, 0
	}
	if w.commandBusy.Load() {
		return w.webBusyResult("A command is running. Wait for it to finish."), false, 0
	}
	z, ok := w.v2PauseNow(boardNow())
	if !ok {
		return staleCard("The paused card closed. Refresh the page."), false, 0
	}
	if a.Choice == "pause/continue" && !z.wait && z.enter.action != marshal.PauseUsePlan {
		if err := w.marshalSession().busyErrorIfBusy("paused_enter"); err != nil {
			return refusedErr(err), false, 0
		}
	}
	before := w.outputSeq.Load()
	w.runPauseAction(a.Choice, z)
	seq := w.outputSeq.Load()
	res := webboard.Result{Status: "done"}
	if seq != before {
		w.mu.RLock()
		res.Text = w.answer
		bad, receipt := w.answerIsError, w.lastRefusal
		w.mu.RUnlock()
		if bad && a.Choice != "pause/details" {
			res.Status = "refused"
			if receipt != nil {
				res = res.WithRefusal(receipt.Clone())
			}
		}
	}
	if res.Status == "done" {
		w.RecordActivity("From the web: " + f.Paused.Actions[index].Label + " (" + f.Paused.Title + ").")
	}
	return res, w.commandBusy.Load(), seq
}
