package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/marshal"
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
		return webActed{res: w.webSetting(a)}
	}
	if a.Kind == "key" && (a.Key == webKeyRetry || a.Key == webKeyStopAll) {
		return w.webScreenAct(a)
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
	case "key":
	default:
		return refused("Unknown action."), false, 0
	}
	if !slices.Contains(webboard.ActionKeys(f), a.Key) {
		return refused("That button is not on the Board now."), false, 0
	}
	if f.Decision != nil && f.Decision.Kind == decisionAsk {
		card := w.currentAsk()
		if card == nil {
			return refused("The question is no longer open."), false, 0
		}
		key := a.Key
		if key == "⏎" || key == "click" {
			key = "enter"
		}
		r := w.answerAskBound(context.Background(), askAnswerBinding{Project: card.Project, ID: card.ID, Launch: card.Launch, Revision: card.Revision, Actor: "owner:web", Key: key})
		if r.Err != nil {
			return webboard.Result{Status: r.Status, Text: r.Err.Error()}, false, 0
		}
		return webboard.Result{Status: "done", Text: "Answer recorded."}, false, 0
	}
	label, title := webActionLabel(f, a.Key), webCardTitle(f)
	switch {
	case a.Key == "q":
		return refused("Quit only from the terminal Board."), false, 0
	case f.Paused != nil && a.Key == "x" && !a.Confirmed:
		return refused("Press Stop the run again to confirm."), false, 0
	}
	if w.commandBusy.Load() {
		return w.webBusyResult("A command is still running on the Board; try again when it finishes."), false, 0
	}
	if f.Paused != nil && a.Key == "⏎" {
		if z, ok := w.v2PauseNow(boardNow()); ok && !z.wait && z.enter.action != marshal.PauseUsePlan {
			if busyErr := w.marshalSession().busyErrorIfBusy("paused_enter"); busyErr != nil {
				return refusedErr(busyErr), false, 0
			}
		}
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
			return refused("That button works only in the terminal Board."), false, 0
		}
		if a.Text != "" {
			// A hard gate's confirmation reads the canonical id: typed
			// into the confirmation as the terminal does, then Enter.
			if !w.inboxView.Confirming() || !w.inboxView.WantsText() || ev.Type != KeyEnter {
				return refused("Nothing on the Board asks for typed text now."), false, 0
			}
			for _, r := range a.Text {
				w.handleV2CardKey(KeyEvent{Type: KeyRune, Rune: r})
			}
		}
		took = w.handleV2CardKey(ev)
	}
	if !took {
		return refused("The Board did not take that button now (is a view open over it?). Nothing was done."), false, 0
	}
	res := webboard.Result{Status: "done"}
	started = w.commandBusy.Load()
	seq = w.outputSeq.Load()
	if seq != before {
		w.mu.RLock()
		text, isErr := w.answer, w.answerIsError
		refusal := w.lastRefusal
		w.mu.RUnlock()
		res.Text = text
		// The raw error is shown as an error on purpose; any other error
		// answer means the key did not do what the button says.
		if isErr && a.Key != "d" {
			out := refused(text)
			if refusal != nil {
				out = out.WithRefusal(refusal.Clone())
			}
			return out, false, 0
		}
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
	return res, started, seq
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

// webActionLabel is the button's label on the card.
func webActionLabel(f webboard.Frame, key string) string {
	var ks []webboard.Key
	switch {
	case f.Decision != nil:
		ks = f.Decision.Actions
	case f.Paused != nil:
		ks = f.Paused.Actions
	default:
		ks = f.Keys
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

// The two Board keys the page offers when no card is shown: r (retry the
// stuck task under the keyboard) and Ctrl+X (stop all workers, never the
// Marshal). They go in the frame's key list (webScreenKeys), so they get
// action tokens like a card's keys, and the page asks for a second click
// on both.
const (
	webKeyRetry   = "r"
	webKeyStopAll = "ctrl+x"
)

// webScreenKeys are the screen's own buttons for the page, when no card is
// shown: Retry <task> while r has a stuck task to retry (as the key bar
// offers r), and Stop all workers while a run is on the Board.
func (w *Workspace) webScreenKeys() []vm.Key {
	var keys []vm.Key
	if id := w.webRetryShown(); id != "" {
		keys = append(keys, vm.Key{Key: webKeyRetry, Label: "retry " + cleanBoard(id)})
	}
	if p := w.marshalPanel(); p != nil && (w.runtime == nil || len(w.webRunLanes(p.RunID)) > 0) {
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

// webScreenAct is r or Ctrl+X from the page, through the keys' own
// handlers: r arms the retry of the task retryTarget names and Enter runs
// it (handleRetryKey: retryAndContinue); Ctrl+X runs /stop all as the key
// does. Both need the page's second click.
func (w *Workspace) webScreenAct(a webboard.Action) webActed {
	out := func(r webboard.Result) webActed { return webActed{res: r} }
	b := w.webBoardFrame()
	f := webboard.FromBoard(b)
	if webboard.CardID(f) != a.Card {
		return out(webboard.Result{Status: "stale", Text: "The Board changed since this page showed it; look again."})
	}
	if !slices.Contains(webboard.ActionKeys(f), a.Key) {
		return out(refused("That button is not on the Board now."))
	}
	label := webActionLabel(f, a.Key)
	if !a.Confirmed {
		return out(refused("Press " + label + " again to confirm."))
	}
	if w.commandBusy.Load() {
		return out(w.webBusyResult("A command is still running on the Board; try again when it finishes."))
	}
	before := w.outputSeq.Load()
	r := webActed{res: webboard.Result{Status: "done"}}
	switch a.Key {
	case webKeyRetry:
		if w.runtime != nil {
			return out(refused("Refresh the page and use the bound task retry control."))
		}
		if busyErr := w.marshalSession().busyErrorIfBusy("retry"); busyErr != nil {
			return out(refusedErr(busyErr))
		}
		id, why := retryTarget(w)
		if why != "" {
			return out(refused(why))
		}
		if "retry "+cleanBoard(id) != label {
			return out(webboard.Result{Status: "stale", Text: "The task to retry changed; look again."})
		}
		w.armRetry(id)
		w.handleRetryKey(KeyEvent{Type: KeyEnter})
		r.started = w.commandBusy.Load()
	case webKeyStopAll:
		if w.runtime != nil {
			// The same admission as the typed control: the run's workers
			// are stopped only through the bound stop-workers op.
			return out(refused("Refresh the page and use the bound stop-workers control."))
		}
		w.runCommand(w.workers.context(), "/stop all")
		r.output = w.uiEvents != nil && w.outputSeq.Load() == before
	}
	r.seq = w.outputSeq.Load()
	if r.seq != before {
		w.mu.RLock()
		text, isErr := w.answer, w.answerIsError
		refusal := w.lastRefusal
		w.mu.RUnlock()
		if isErr {
			res := refused(text)
			if refusal != nil {
				res = res.WithRefusal(refusal.Clone())
			}
			return out(res)
		}
		r.res.Text = text
	}
	w.RecordActivity("From the web: " + label + ".")
	return r
}
