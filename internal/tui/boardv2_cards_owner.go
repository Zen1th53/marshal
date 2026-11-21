package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The owner cards as Board v2 decision cards: the changed-files guard, the
// "what next?" card of a closed run, the Marshal's question, the
// requirements card and the mid-run change. Each is the classic inbox card
// (board_guard.go, board_next.go, board_ask.go, board_requirements.go,
// board_change.go) drawn over the dimmed Board: the same facts, the same
// keys and the same click actions. A key goes to the classic card's own key
// handler and a click on a chip or a line to its own click handler, so the
// card does exactly what the classic one does (handleV2OwnerKey,
// v2OwnerHit).

// Owner card kinds, after the decision kinds.
const (
	decisionGuard        = "guard"
	decisionNext         = "next"
	decisionAsk          = "ask"
	decisionRequirements = "requirements"
	decisionChange       = "change"
)

// ownerCardKind reports whether kind is one of the owner cards.
func ownerCardKind(kind string) bool {
	switch kind {
	case decisionGuard, decisionNext, decisionAsk, decisionRequirements, decisionChange:
		return true
	}
	return false
}

// v2OwnerCards is every owner card open now, in the order the classic Board
// takes their keys: the guard's alert, "what next?", the question, the
// requirements, the change.
func (w *Workspace) v2OwnerCards() []v2Decision {
	var out []v2Decision
	enter := "⏎"
	if w.boardStopWaiting() {
		// A stopped run takes Enter, as on the classic card: the button
		// is clicked, not keyed.
		enter = "click"
	}
	if a, prefix, armed, undo := w.guardPaint(); a != nil {
		out = append(out, guardDecision(a, prefix, armed, undo))
	}
	if c := w.currentNext(); c != nil {
		out = append(out, nextDecision(*c))
	}
	if c := w.currentAsk(); c != nil {
		out = append(out, askDecision(*c, enter))
	}
	w.req.mu.Lock()
	var req *reqCard
	if w.req.card != nil {
		c := *w.req.card
		req = &c
	}
	w.req.mu.Unlock()
	if req != nil {
		out = append(out, requirementsDecision(*req, enter))
	}
	w.change.mu.Lock()
	var change *changeCard
	if w.change.card != nil {
		c := *w.change.card
		c.held = append([]string(nil), c.held...)
		change = &c
	}
	w.change.mu.Unlock()
	if change != nil {
		out = append(out, changeDecision(*change, enter))
	}
	return out
}

// ownerAction is one chip of an owner card and what a click on it reaches.
type ownerAction struct{ key, label, ref string }

func (d *v2Decision) setActions(as ...ownerAction) {
	d.card.Actions, d.card.Refs = nil, nil
	for _, a := range as {
		d.card.Actions = append(d.card.Actions, vm.Key{Key: a.key, Label: a.label})
		d.card.Refs = append(d.card.Refs, a.ref)
	}
}

// guardDecision is the changed-files alert: which files changed and who
// changed them, keep or undo; while the undo is armed, what it would do.
func guardDecision(a *guardAlert, prefix string, armed bool, u guardUndo) v2Decision {
	c := vm.Decision{Kind: decisionGuard, Title: "Guard · project files changed outside a run", Summary: "workers should implement; the Marshal was told not to"}
	if a.byMarshal {
		c.Title = "Guard · the Marshal changed project files"
		c.Summary = "only workers may change them; undo is recommended"
	}
	if a.answered != "" {
		c.ChangedNote = "you answered “" + cleanBoard(a.answered) + "”; nothing is undone yet"
	}
	const shown = 6
	for i, p := range a.paths {
		if i == shown {
			c.Lines = append(c.Lines, vm.CardLine{Text: fmt.Sprintf("and %d more", len(a.paths)-shown), Tone: vm.ToneMuted})
			break
		}
		if prefix != "" {
			p = strings.TrimPrefix(p, prefix+"/")
		}
		c.Lines = append(c.Lines, vm.CardLine{Text: cleanBoard(p)})
	}
	d := v2Decision{kind: decisionGuard, key: "guard:" + strconv.FormatInt(a.at.UnixNano(), 10), card: c}
	if armed {
		var what []string
		if n := len(u.remove); n > 0 {
			what = append(what, fmt.Sprintf("delete %d new %s", n, plural(n, "file", "files")))
		}
		if n := len(u.restore); n > 0 {
			what = append(what, fmt.Sprintf("restore %d to HEAD", n))
		}
		d.card.Lines = append(d.card.Lines, vm.CardLine{Text: "undo: " + strings.Join(what, ", ") + ", nothing else?", Tone: vm.ToneWarn})
		d.card.Corner = "esc cancel"
		d.setActions(ownerAction{"⏎", "undo", "guard:u"}, ownerAction{"esc", "cancel", "guard:n"})
		return d
	}
	undo := "undo"
	if a.byMarshal {
		undo = "undo (recommended)"
	}
	d.setActions(ownerAction{"u", undo, "guard:u"}, ownerAction{"K", "keep", "guard:K"})
	return d
}

// nextDecision is the "what next?" card of a closed run: the runtime's
// counts and the keys for the next goal.
func nextDecision(c nextCard) v2Decision {
	d := v2Decision{kind: decisionNext, key: "next:" + c.RunID, card: vm.Decision{
		Kind: decisionNext, Title: "What next?", Corner: "run done", Summary: c.line(),
		Lines: []vm.CardLine{{Text: "The Marshal's report on this run is in the chat. Nothing starts until you give the next goal.", Tone: vm.ToneMuted}},
	}}
	d.setActions(ownerAction{"n", "new goal", "next:n"}, ownerAction{"d", "details", "next:d"}, ownerAction{"q", "quit", "next:q"})
	return d
}

// askDecision is the Marshal's question with one chip per answer; enter is
// what the first answer's key reads as.
func askDecision(c askCard, enter string) v2Decision {
	q := cleanBoard(strings.Join(strings.Fields(c.Question), " "))
	d := v2Decision{kind: decisionAsk, key: "ask:" + c.digest, card: vm.Decision{
		Kind: decisionAsk, Title: "Marshal asks · " + firstLine(c.Question),
		Summary: "your answer goes to the Marshal chat as your own message",
		Lines:   []vm.CardLine{{Text: q}},
	}}
	if c.guard != nil {
		d.card.ChangedNote = "it decides the changed files"
	}
	var as []ownerAction
	for i, ch := range c.Choices {
		key := ch.Key
		if key == "enter" {
			key = enter
		}
		as = append(as, ownerAction{key, ch.Label, "ask:" + strconv.Itoa(i)})
	}
	d.setActions(as...)
	return d
}

// requirementsDecision is the a–h requirements the Marshal filled, each with
// its source: Enter confirms all, e then a letter (or a click on the line)
// edits one.
func requirementsDecision(c reqCard, enter string) v2Decision {
	filled := c.filled()
	d := v2Decision{kind: decisionRequirements, key: "requirements:" + strconv.FormatInt(c.At.UnixNano(), 10), card: vm.Decision{
		Kind: decisionRequirements, Title: fmt.Sprintf("Confirm requirements · %d of 8", filled),
		Summary: "the Marshal filled these from the code and memory",
	}}
	if blank := 8 - filled; blank > 0 {
		d.card.ChangedNote = fmt.Sprintf("%d blank: it asks them one at a time", blank)
	}
	for i, l := range c.Lines {
		line := vm.CardLine{Key: l.Line, Label: reqLabels[i], Text: cleanBoard(l.Text), Note: "· " + cleanBoard(l.Source), Ref: "requirements:" + l.Line}
		if l.Text == "" {
			line.Text, line.Note, line.Tone = "—", "· (to ask)", vm.ToneMuted
		}
		d.card.Lines = append(d.card.Lines, line)
	}
	if c.editing {
		d.card.ChangedNote = "Which line? Press a–h or click it."
		d.card.Corner = "esc cancel"
		d.setActions(ownerAction{"esc", "cancel", ""})
		return d
	}
	d.setActions(ownerAction{enter, "confirm all", "requirements:enter"}, ownerAction{"e", "edit a line", "requirements:edit"})
	return d
}

// changeDecision is the owner's mid-run change: what the hold paused, apply
// it or keep the current goal; or the owner's own hold and its release.
func changeDecision(c changeCard, enter string) v2Decision {
	d := v2Decision{kind: decisionChange, key: "change:" + c.runID + ":" + strconv.FormatInt(c.at.UnixNano(), 10), card: vm.Decision{Kind: decisionChange}}
	switch {
	case c.ownerHold():
		d.card.Title = "Change · held by you"
		d.card.Summary = ownerHoldHeadline(c.held)
		d.setActions(ownerAction{"x", "release the hold", "change:release"})
	case c.timedOut:
		d.card.Title = "Change · the run could not pause"
		d.card.Summary = "I couldn't pause the run to apply the change. Press Enter to try again."
		d.setActions(ownerAction{enter, "try again", "change:apply"})
	case c.applying:
		d.card.Title = "Change · applying"
		d.card.Corner = "wait"
		d.card.Summary = "Applying your change…"
	default:
		d.card.Title = "Change · apply it or keep the current goal"
		d.card.Summary = changeHoldHeadline(c.notStarted, c.running)
		d.card.Lines = []vm.CardLine{{Text: "Apply tells the Marshal to amend the plan for your change; keep releases the paused tasks and the run goes on as planned.", Tone: vm.ToneMuted}}
		d.setActions(ownerAction{enter, "apply this change", "change:apply"}, ownerAction{"k", "keep the current goal", "change:keep"})
	}
	return d
}

// handleV2OwnerKey is an owner card's keys: the classic card's own key
// handler takes them first; Esc it does not take puts the card off
// ("later") while it waits, and the inbox still lists it.
func (w *Workspace) handleV2OwnerKey(event KeyEvent, d v2Decision) bool {
	var took bool
	switch d.kind {
	case decisionGuard:
		took = w.handleGuardKey(event)
	case decisionNext:
		took = w.handleNextKey(event)
	case decisionAsk:
		took = w.handleAskKey(event)
	case decisionRequirements:
		took = w.handleRequirementsKey(event)
	case decisionChange:
		took = w.handleChangeKey(event)
	}
	if took {
		return true
	}
	if event.Type == KeyEsc {
		w.dismissV2Card(d.key)
		return true
	}
	return false
}

// v2OwnerHit is the classic region an owner card's chip or line reference
// stands for ("ask:1", "requirements:c", "guard:K"), so a click runs the
// classic card's click handler; false for none.
func v2OwnerHit(out boardHit, ref string) (boardHit, bool) {
	kind, id, ok := strings.Cut(ref, ":")
	if !ok || id == "" {
		return out, false
	}
	switch kind {
	case decisionAsk:
		out.kind = hitAsk
	case decisionRequirements:
		out.kind = hitRequirements
	case decisionChange:
		out.kind = hitChange
	case decisionNext:
		out.kind = hitNext
	case decisionGuard:
		out.kind, out.action = hitGuard, rune(id[0])
		id = "guard"
	default:
		return out, false
	}
	out.id = id
	return out, true
}
