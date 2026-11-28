package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/board/vm"
	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// Typed decisions from the Web Board. Every decision card names its actions
// by typed id (decisionChoices) and carries a bound revision
// (decisionRevision). The page sends a choice with the revision it showed;
// webDecide checks the choice and the revision against the card the Board
// shows now and runs the same handler the terminal key or click reaches. A
// legacy key the card offers resolves to its typed choice first
// (legacyChoice), so both paths share one authority check.
//
//	change.apply|keep|release   the change card's own click actions
//	requirements.confirm        confirm all lines (refused while blanks remain)
//	requirements.edit           the owner's edit of one line, as the owner's
//	                            chat words; it moves the card's revision
//	plan.read, plan.approve     approval binds the exact plan digest the owner
//	                            opened, and the reply waits for a confirmation
//	inbox.inspect               the exact request, read-only
//	inbox.decide, inbox.return  the inbox's own confirmation, on the request
//	                            the card names, with the typed id of a hard
//	                            gate and a return's reason
//	card.choose                 an ask, guard or "what next?" choice
//	architecture.open           the review in the browser

// decisionRevision is the bound revision of d: its key, and for the
// requirements card the count of edits sent from it.
func (w *Workspace) decisionRevision(d v2Decision) string {
	if d.kind == decisionRequirements {
		if c := w.currentReq(); c != nil {
			return d.key + ":" + strconv.Itoa(c.edits)
		}
	}
	if d.kind == decisionPlan && d.card.Plan != nil {
		return d.key + ":" + d.card.Plan.Digest
	}
	return d.key
}

// decisionChoices is every action of d by typed id. A key with no Web
// meaning (copy a link, quit) is left out.
func decisionChoices(d v2Decision) []vm.Choice {
	out := []vm.Choice{}
	for i, k := range d.card.Actions {
		ref := ""
		if i < len(d.card.Refs) {
			ref = d.card.Refs[i]
		}
		c := vm.Choice{Key: k.Key, Label: k.Label}
		switch d.kind {
		case decisionChange:
			c.ID = "change." + strings.TrimPrefix(ref, decisionChange+":")
		case decisionRequirements:
			switch ref {
			case "requirements:enter":
				c.ID = "requirements.confirm"
			case "requirements:edit":
				c.ID = "requirements.edit"
			}
		case decisionAsk, decisionGuard, decisionNext:
			if ref != "" && ref != "next:q" {
				c.ID, c.Value = "card.choose", ref
			}
		case decisionPlan:
			switch k.Key {
			case "⏎":
				c.ID = "plan.approve"
			case "7":
				c.ID = "plan.read"
			case "k":
				c.ID, c.Local = "plan.comment", true
			}
		case decisionArchitecture:
			switch k.Key {
			case "a":
				c.ID = "architecture.open"
			case "k":
				c.ID, c.Local = "architecture.comment", true
			}
		case decisionInbox:
			switch k.Key {
			case "a":
				c.ID, c.Value = "inbox.decide", "allow"
			case "d":
				c.ID, c.Value = "inbox.decide", "deny"
			case "⏎":
				c.ID, c.Label = "inbox.inspect", "inspect the request"
			}
		}
		if c.ID != "" {
			out = append(out, c)
		}
	}
	if d.kind == decisionInbox {
		out = append(out, vm.Choice{ID: "inbox.return", Key: "r", Label: "return with a reason"})
	}
	return out
}

// planPreviewOf is run's drafted plan as the owner reads it, with the
// digest an approval must carry.
func planPreviewOf(run marshal.Run) *vm.PlanPreview {
	p := &vm.PlanPreview{ID: run.PlanID, Version: run.PlanVersion, Tasks: []vm.PlanTask{}}
	for _, t := range run.Tasks {
		pt := vm.PlanTask{ID: t.PlanTaskID, Title: t.Title, DependsOn: slices.Clone(t.DependsOn), Files: slices.Clone(t.Files), Criteria: slices.Clone(t.Criteria)}
		for _, c := range t.Checks {
			pt.Checks = append(pt.Checks, c.Command)
		}
		p.Tasks = append(p.Tasks, pt)
	}
	// The digest binds what is shown and what the approved tasks carry
	// besides: their instructions, expected output and check criteria.
	type bound struct {
		Instructions, ExpectedOutput string
		Checks                       []marshal.Check
		Components                   []string
	}
	extra := make([]bound, 0, len(run.Tasks))
	for _, t := range run.Tasks {
		extra = append(extra, bound{t.Instructions, t.ExpectedOutput, t.Checks, t.Components})
	}
	raw, _ := json.Marshal(struct {
		Preview *vm.PlanPreview
		Tasks   []bound
		Scope   string
	}{p, extra, run.ApprovalScopeDigest})
	sum := sha256.Sum256(raw)
	p.Digest = hex.EncodeToString(sum[:16])
	return p
}

// legacyChoice is the typed choice a legacy key of f's decision card
// presses, false for a key with none (esc, a local action) or while a
// confirmation the key answers is open.
func (w *Workspace) legacyChoice(f webboard.Frame, key string) (webboard.Action, bool) {
	d := f.Decision
	if d == nil || (d.Kind == decisionInbox && w.inboxView.Confirming()) {
		return webboard.Action{}, false
	}
	for _, c := range d.Choices {
		if c.Key != key || c.Local {
			continue
		}
		a := webboard.Action{Kind: "decide", Card: webboard.CardID(f), Choice: c.ID, Value: c.Value, Revision: d.Revision}
		if d.Plan != nil {
			// The card id binds the plan the page showed.
			a.Digest = d.Plan.Digest
		}
		return a, true
	}
	return webboard.Action{}, false
}

func staleCard(text string) webboard.Result {
	return webboard.Result{Status: "stale", Text: text}
}

// webAnswer is the Board's answer since before, if the handler gave one.
func (w *Workspace) webAnswer(before uint64) (string, *webboard.Result) {
	if w.outputSeq.Load() == before {
		return "", nil
	}
	w.mu.RLock()
	text, isErr := w.answer, w.answerIsError
	refusal := w.lastRefusal
	w.mu.RUnlock()
	if !isErr {
		return text, nil
	}
	out := refused(text)
	if refusal != nil {
		out = out.WithRefusal(refusal.Clone())
	}
	return text, &out
}

// webDecide runs a typed choice on the input loop, against the card the
// Board shows now. Nothing runs when the revision, the choice or the plan
// digest no longer match.
func (w *Workspace) webDecide(f webboard.Frame, a webboard.Action) webboard.Result {
	d := f.Decision
	if d == nil {
		return refused("No decision card is on the Board now.")
	}
	if a.Revision != d.Revision {
		return staleCard("The card changed since this page showed it; look again.")
	}
	i := slices.IndexFunc(d.Choices, func(c webboard.Choice) bool { return c.ID == a.Choice && c.Value == a.Value })
	if i < 0 {
		return refused("That choice is not on the card now.")
	}
	c := d.Choices[i]
	if c.Local {
		return refused("That choice only changes what the page shows.")
	}
	cur, ok := w.v2DecisionNow(time.Now())
	if !ok || cur.kind != d.Kind || w.decisionRevision(cur) != d.Revision {
		return staleCard("The card changed since this page showed it; look again.")
	}
	if w.commandBusy.Load() {
		return w.webBusyResult("A command is still running on the Board; try again when it finishes.")
	}
	before := w.outputSeq.Load()
	res := w.webDecideNow(cur, d, c, a)
	if res.Status == "done" && res.Confirm == nil {
		line := "From the web: " + c.Label
		if d.Title != "" {
			line += " (" + d.Title + ")"
		}
		w.RecordActivity(line + ".")
	}
	if res.Status == "done" && res.Text == "" {
		text, bad := w.webAnswer(before)
		if bad != nil {
			return *bad
		}
		res.Text = text
	}
	return res
}

func (w *Workspace) webDecideNow(cur v2Decision, d *webboard.Decision, c webboard.Choice, a webboard.Action) webboard.Result {
	done := webboard.Result{Status: "done"}
	before := w.outputSeq.Load()
	answered := func() webboard.Result {
		text, bad := w.webAnswer(before)
		if bad != nil {
			return *bad
		}
		if text != "" {
			done.Text = text
		}
		return done
	}
	switch c.ID {
	case "change.apply", "change.keep", "change.release":
		if w.changeApplying() {
			return refused("The change is being applied; wait for it to settle.")
		}
		w.clickChange(strings.TrimPrefix(c.ID, "change."))
		done.Text = cap1(c.Label) + "."
		return answered()
	case "requirements.confirm":
		card := w.currentReq()
		if card == nil {
			return staleCard("The requirements card is closed.")
		}
		if card.filled() < 8 {
			return refused("Answer the blank lines first; the Marshal asks them one at a time.")
		}
		w.confirmRequirements()
		done.Text = "You confirmed the requirements."
		return answered()
	case "requirements.edit":
		return w.webEditRequirement(cur, a)
	case "plan.read", "plan.approve":
		return w.webPlanChoice(cur, d, c, a)
	case "inbox.inspect":
		item, ok := w.inboxItemFor(d.Revision)
		if !ok {
			return staleCard("That request is no longer pending.")
		}
		r := item.Request
		parts := []string{"id " + item.Canonical, "what " + strings.TrimSpace(r.Kind+" "+r.Object)}
		if r.Scope != "" {
			parts = append(parts, "scope "+r.Scope)
		}
		if r.Reason != "" {
			parts = append(parts, "reason "+r.Reason)
		}
		done.Text = cleanBoard(strings.Join(parts, " · "))
		return done
	case "inbox.decide", "inbox.return":
		return w.webInboxChoice(d, c, a)
	case "card.choose":
		if cur.kind == decisionAsk {
			card := w.currentAsk()
			n, err := strconv.Atoi(strings.TrimPrefix(c.Value, "ask:"))
			if card == nil || err != nil || n < 0 || n >= len(card.Choices) {
				return staleCard("The question is no longer open.")
			}
			r := w.answerAskBound(w.workers.context(), askAnswerBinding{Project: card.Project, ID: card.ID, Launch: card.Launch, Revision: card.Revision, Actor: "owner:web", Key: card.Choices[n].Key})
			if r.Err != nil {
				return webboard.Result{Status: r.Status, Text: r.Err.Error()}
			}
			done.Text = "Answer recorded."
			return done
		}
		if !slices.Contains(cur.card.Refs, c.Value) || !w.clickOwnerRef(c.Value) {
			return refused("The Board did not take that choice now. Nothing was done.")
		}
		return answered()
	case "architecture.open":
		w.openArchitectureReview()
		out := answered()
		if out.Status == "done" {
			out.Link = w.architectureLink()
		}
		return out
	}
	return refused("That choice works only in the terminal Board.")
}

func cap1(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// webEditRequirement sends the owner's new text for one line as the owner's
// words ("edit c: …"), the message the terminal's edit composer sends. The
// card's revision moves, so a second edit made against the same view is
// refused.
func (w *Workspace) webEditRequirement(cur v2Decision, a webboard.Action) webboard.Result {
	if len(a.Line) != 1 || a.Line[0] < 'a' || a.Line[0] > 'h' {
		return refused("Name the line to edit, a to h.")
	}
	text := strings.Join(strings.Fields(a.Text), " ")
	switch {
	case text == "":
		return refused("Write the line's new text.")
	case len(text) > 200:
		return refused("A requirement line is at most 200 bytes.")
	case strings.ContainsFunc(text, unicode.IsControl):
		return refused("A requirement line has no control characters.")
	}
	if w.sayPane() == "" && !w.nativeChatShown() {
		return refused("No Marshal chat is open, so nothing was sent. /marshal chat in the terminal starts one.")
	}
	rev := w.decisionRevision(cur)
	w.req.mu.Lock()
	if w.req.card == nil || cur.key+":"+strconv.Itoa(w.req.card.edits) != rev {
		w.req.mu.Unlock()
		return staleCard("The requirements card changed; look again.")
	}
	w.req.card.edits++
	w.req.card.editing = false
	w.req.mu.Unlock()
	w.sayToMarshal(w.workers.context(), fmt.Sprintf("edit %c: %s", a.Line[0], text))
	w.requestRepaint()
	return webboard.Result{Status: "done", Text: fmt.Sprintf("Sent your edit of line %c to the Marshal; the card updates when it answers.", a.Line[0])}
}

// webPlanChoice is plan.read, which records the digest the owner opened,
// and plan.approve, which needs that same digest to be the current plan's,
// then arms the reply and sends it on the confirmation.
func (w *Workspace) webPlanChoice(cur v2Decision, d *webboard.Decision, c webboard.Choice, a webboard.Action) webboard.Result {
	if d.Plan == nil || cur.card.Plan == nil || cur.card.Plan.Digest != d.Plan.Digest {
		return staleCard("The plan changed; read it again.")
	}
	s := w.v2CardState()
	if c.ID == "plan.read" {
		s.mu.Lock()
		s.planViewed = d.Plan.Digest
		s.mu.Unlock()
		return webboard.Result{Status: "done", Text: "The plan is open on the page; approving sends your reply for this exact plan."}
	}
	if a.Digest != d.Plan.Digest {
		return staleCard("The plan changed since you read it; read it again.")
	}
	s.mu.Lock()
	viewed, armed := s.planViewed, s.planArmed
	s.mu.Unlock()
	if viewed != d.Plan.Digest {
		return refused("Read the plan before approving it.")
	}
	if cur.gate.sent {
		w.armGate(cur.gate)
		return webboard.Result{Status: "done"}
	}
	if armed != cur.key {
		s.mu.Lock()
		s.planArmed = cur.key
		s.mu.Unlock()
		w.requestRepaint()
		prompt := "Send “" + cur.gate.reply + "” to the Marshal as your reply for this plan."
		return webboard.Result{Status: "done", Text: prompt, Confirm: &webboard.Confirm{Label: "Send the reply", Prompt: prompt}}
	}
	s.mu.Lock()
	s.planArmed = ""
	s.mu.Unlock()
	before := w.outputSeq.Load()
	w.sendGate(w.workers.context(), cur.gate)
	text, bad := w.webAnswer(before)
	if bad != nil {
		return *bad
	}
	return webboard.Result{Status: "done", Text: text}
}

// inboxItemFor is the pending request the inbox card with revision names.
func (w *Workspace) inboxItemFor(revision string) (boardmodel.InboxItem, bool) {
	canonical, ok := strings.CutPrefix(revision, decisionInbox+":")
	if !ok {
		return boardmodel.InboxItem{}, false
	}
	for _, it := range w.boardInbox() {
		if it.Canonical == canonical {
			return it, true
		}
	}
	return boardmodel.InboxItem{}, false
}

// webInboxChoice is allow, deny or return on the request the card names,
// through the inbox's own confirmation: the first press begins it, the
// confirmation types what it asks for (a hard gate's id, a return's
// reason) and decides.
func (w *Workspace) webInboxChoice(d *webboard.Decision, c webboard.Choice, a webboard.Action) webboard.Result {
	item, ok := w.inboxItemFor(d.Revision)
	if !ok {
		return staleCard("That request is no longer pending.")
	}
	action := 'r'
	switch c.Value {
	case "allow":
		action = 'a'
	case "deny":
		action = 'd'
	}
	v := &w.inboxView
	v.Update(w.boardInbox())
	if !v.Select(item.Request.Key()) {
		return staleCard("That request is no longer pending.")
	}
	confirm := func() webboard.Result {
		prompt := v.Prompt()
		return webboard.Result{Status: "done", Text: prompt, Confirm: &webboard.Confirm{Label: "Confirm " + c.Label, Prompt: prompt, Text: v.WantsText()}}
	}
	if !a.Confirmed {
		v.Cancel()
		v.Begin(action)
		w.requestRepaint()
		return confirm()
	}
	if v.Action() != action {
		return refused("Press " + c.Label + " again; its confirmation is no longer open.")
	}
	if v.WantsText() {
		text := a.Text
		if text == "" {
			text = a.Reason
		}
		v.ClearInput()
		v.Input(text)
	}
	dec, ok := v.Confirm()
	if !ok {
		if v.Confirming() && strings.HasPrefix(v.Prompt(), "Reason then Enter: ") && strings.TrimSpace(a.Text+a.Reason) != "" && item.Gate == boardmodel.GateTyped {
			// The hard gate's id matched; the return's reason is next.
			return confirm()
		}
		v.ClearInput()
		return refused("That does not match what the Board asks for; nothing was decided.")
	}
	if err := w.decideInbox(w.workers.context(), dec); err != nil {
		return refused("Permission decision failed: " + err.Error())
	}
	w.requestRepaint()
	return webboard.Result{Status: "done", Text: cap1(c.Label) + ": decided."}
}
