package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The decision pane: the waiting decision or paused card drawn in full on
// the terminal Home and acted on from explicit decision focus (o). It reads
// the same decision state the Web Board reads (decisionChoices,
// decisionRevision), shows every choice, what Enter does, when a limit
// resets and which tasks and evidence stay, and binds each key to the card
// it showed: when the card changes, the pane closes instead of acting on
// the new one. Enter opens the chat on the Home and acts only here. Keys
// are routed in this order: the editor, an inbox confirmation, an armed
// reply or stop, the card's own keys, then the global keys. No key accepts
// the architecture; its review is opened or copied from the card.

// The cards decision focus rests on.
const (
	focusDecision = "decision"
	focusPaused   = "paused"
)

// pausedRevision is the identity of paused card z a key is bound to: its
// cause, the tasks it names and its reset, not the text that fills in later.
func pausedRevision(z v2Pause) string {
	var ids []string
	for _, t := range z.tasks {
		ids = append(ids, t.PlanTaskID)
	}
	slices.Sort(ids)
	return "paused:" + z.cause + ":" + strings.Join(ids, ",")
}

// decisionFocusReady reports whether a card waits that o can focus.
func (w *Workspace) decisionFocusReady() bool {
	if !w.boardVisible() || w.anyBodyView() {
		return false
	}
	_, _, ok := w.decisionFocusTarget("")
	return ok
}

// decisionFocusTarget is the card to focus: want when it is shown, else
// the decision, else the paused card; with its bound revision.
func (w *Workspace) decisionFocusTarget(want string) (kind, rev string, ok bool) {
	now := time.Now()
	d, dok := w.v2DecisionNow(now)
	z, zok := w.v2PauseNow(now)
	if want == focusPaused && zok || !dok && zok {
		return focusPaused, pausedRevision(z), true
	}
	if dok {
		return focusDecision, w.decisionRevision(d), true
	}
	return "", "", false
}

// openDecisionFocus puts decision focus on the waiting card.
func (w *Workspace) openDecisionFocus(want string) bool {
	kind, rev, ok := w.decisionFocusTarget(want)
	if !ok {
		w.setOutput("Nothing waits for you in a card.", false)
		return false
	}
	ui := &w.boardUI
	ui.homePage = ""
	ui.decisionFocus, ui.decisionRev, ui.decisionStop = kind, rev, false
	w.requestRepaint()
	return true
}

// dropDecisionFocus leaves focus without touching a confirmation; a key sent
// from the palette or a click acts as itself, not on the focused card.
func (w *Workspace) dropDecisionFocus() {
	ui := &w.boardUI
	ui.decisionFocus, ui.decisionRev, ui.decisionStop = "", "", false
}

// closeDecisionFocus leaves decision focus and cancels what it armed.
func (w *Workspace) closeDecisionFocus() {
	ui := &w.boardUI
	ui.decisionFocus, ui.decisionRev, ui.decisionStop = "", "", false
	if w.inboxView.Confirming() {
		w.inboxView.Cancel()
	}
	w.v2CardState().mu.Lock()
	w.v2CardState().planArmed = ""
	w.v2CardState().mu.Unlock()
	w.requestRepaint()
}

// bindInboxCard points the inbox's selection at the request the card names,
// so a key acts on what the card showed and not on whatever the inbox
// selects; it reports false when that request no longer waits.
func (w *Workspace) bindInboxCard(d v2Decision) bool {
	item, ok := w.inboxItemFor(d.key)
	if !ok {
		return false
	}
	v := &w.inboxView
	v.Update(w.boardInbox())
	if v.Confirming() {
		cur, ok := v.Current()
		return ok && cur.Request.Key() == item.Request.Key()
	}
	return v.Select(item.Request.Key())
}

// handleDecisionFocusKey is a key while decision focus is on. It reports
// whether it took the key; a key the card does not document goes on to the
// global keys.
func (w *Workspace) handleDecisionFocusKey(event KeyEvent) bool {
	ui := &w.boardUI
	ctx := w.workers.context()
	kind, rev, ok := w.decisionFocusTarget(ui.decisionFocus)
	if !ok || kind != ui.decisionFocus || rev != ui.decisionRev {
		w.closeDecisionFocus()
		w.setOutput("That card changed; look at it again before deciding (o).", false)
		return true
	}
	now := time.Now()
	if kind == focusDecision {
		d, _ := w.v2DecisionNow(now)
		if d.kind == decisionInbox && !w.bindInboxCard(d) {
			w.closeDecisionFocus()
			w.setOutput("That request is no longer pending.", false)
			return true
		}
		if w.inboxView.Confirming() {
			// The confirmation is modal: Esc cancels it first, and only its
			// own keys type into it.
			w.handleBoardInboxKey(ctx, event)
			return true
		}
		if w.v2PlanArmed() != "" {
			w.handleV2DecisionKey(event, d)
			return true
		}
		switch {
		case event.Type == KeyEsc:
			w.closeDecisionFocus()
			return true
		case event.Type == KeyTab:
			if _, ok := w.v2PauseNow(now); ok {
				w.openDecisionFocus(focusPaused)
			}
			return true
		case d.kind == decisionInbox && isRune(event, 'r'):
			w.inboxView.Begin('r')
			return true
		}
		return w.handleV2DecisionKey(event, d)
	}
	z, _ := w.v2PauseNow(now)
	if ui.decisionStop {
		ui.decisionStop = false
		if event.Type == KeyEnter || isRune(event, 'x') {
			w.stopPausedRun(ctx)
			w.closeDecisionFocus()
		} else {
			w.setOutput("Run not stopped.", false)
		}
		return true
	}
	switch {
	case event.Type == KeyEsc:
		w.closeDecisionFocus()
		return true
	case event.Type == KeyTab:
		if _, ok := w.v2DecisionNow(now); ok {
			w.openDecisionFocus(focusDecision)
		}
		return true
	case isRune(event, 'x'):
		ui.decisionStop = true
		w.setOutput("Press x or Enter again to stop the run; the run and its work are kept, and /marshal resume continues it. Any other key cancels.", false)
		return true
	}
	return w.handleV2PauseKey(event, z)
}

// paintDecisionPane is the pane of the focused card in cols x rows.
func (w *Workspace) paintDecisionPane(v boardVisual, b vm.Board, cols, rows int) []string {
	ui := &w.boardUI
	var body []string
	switch {
	case ui.decisionFocus == focusDecision && b.Decision != nil:
		body = decisionPaneLines(*b.Decision, w.inboxView.Confirming())
	case ui.decisionFocus == focusPaused && b.Paused != nil:
		body = pausedPaneLines(*b.Paused, ui.decisionRev, ui.decisionStop)
	default:
		body = []string{"That card is gone; Esc goes back."}
	}
	both := b.Decision != nil && b.Paused != nil
	keys := "Esc back"
	if both {
		keys = "Tab other card   Esc back"
	}
	var content []string
	content = append(content, v.homeHeader(b.Top, cols), "")
	for _, l := range body {
		for _, p := range wrapWords(l, max(8, cols-2)) {
			content = append(content, " "+p)
		}
		if l == "" {
			content = append(content, "")
		}
	}
	bottom := 1
	if rows >= 4 {
		bottom = 2
	}
	if room := rows - bottom; len(content) > room {
		content = content[:max(0, room)]
	}
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = strings.Repeat(" ", cols)
	}
	for i, s := range content {
		lines[i] = PadCell(Truncate(s, cols), cols)
	}
	if bottom == 2 {
		lines[rows-2] = PadCell(v.fg("dim", " "+Truncate(keys, cols-1)), cols)
	}
	status := v.status
	if status == "" {
		status = v.inboxPrompt
	}
	if status != "" {
		lines[rows-1] = PadCell(" "+Truncate(status, cols-1), cols)
	}
	if !v.unicode() {
		return v.ascii(lines, cols)
	}
	return lines
}

func keyLine(k vm.Key, note string) string {
	s := k.Key + "  " + k.Label
	if note != "" {
		s += "  (" + note + ")"
	}
	return s
}

// decisionPaneLines is the decision card in full: its words, the lines the
// owner reads, the plan with every check, each choice by typed id, what
// Enter does and the revision a key is bound to.
func decisionPaneLines(d vm.Decision, confirming bool) []string {
	out := []string{"DECISION · " + d.Title}
	for _, s := range []string{d.Summary, d.ChangedNote, d.SelfApproval} {
		if s != "" {
			out = append(out, s)
		}
	}
	if d.Link != "" {
		out = append(out, "Review link: "+d.Link)
	}
	out = append(out, "", "CHOICES")
	enter := "nothing here; use a key below"
	typed := map[string]vm.Choice{}
	for _, c := range d.Choices {
		typed[c.Key] = c
	}
	for _, k := range d.Actions {
		note := typed[k.Key].ID
		if c := typed[k.Key]; c.Value != "" {
			note += " " + c.Value
		}
		out = append(out, keyLine(k, note))
		if k.Key == "⏎" {
			enter = k.Label
		}
	}
	if c, ok := typed["r"]; ok && !slices.ContainsFunc(d.Actions, func(k vm.Key) bool { return k.Key == "r" }) {
		out = append(out, keyLine(vm.Key{Key: "r", Label: c.Label}, c.ID))
	}
	if d.Kind == decisionArchitecture {
		out = append(out, "Only you accept the architecture, in the browser: a opens its private review, c copies the link.")
	}
	if confirming {
		out = append(out, "", "A confirmation is open: Enter confirms, Esc cancels.")
	} else {
		out = append(out, "", "Enter: "+enter)
	}
	out = append(out, "Bound to "+d.Revision)
	for _, l := range d.Lines {
		out = append(out, strings.TrimSpace(strings.Join([]string{l.Key, l.Label, l.Text, l.Note}, " ")))
	}
	if p := d.Plan; p != nil {
		out = append(out, "", fmt.Sprintf("PLAN %s v%d · digest %s", p.ID, p.Version, p.Digest))
		for _, t := range p.Tasks {
			head := t.ID + " " + t.Title
			if len(t.DependsOn) > 0 {
				head += " · after " + strings.Join(t.DependsOn, ", ")
			}
			out = append(out, head)
			for _, g := range []struct {
				name string
				list []string
			}{{"files", t.Files}, {"criteria", t.Criteria}, {"checks", t.Checks}} {
				if len(g.list) > 0 {
					out = append(out, "    "+g.name+": "+strings.Join(g.list, "; "))
				}
			}
		}
	}
	return out
}

// pausedPaneLines is the paused card in full.
func pausedPaneLines(p vm.Paused, rev string, stopArmed bool) []string {
	out := []string{"PAUSED · " + strings.TrimPrefix(p.Title, "Paused · ")}
	if p.Cause != "" {
		out = append(out, p.Cause)
	}
	if p.Resets != "" {
		out = append(out, "Resets at "+p.Resets)
	}
	out = append(out, "", "ENTER DOES", strings.ReplaceAll(p.Will, "\n", " "), "", "CHOICES")
	for _, k := range p.Actions {
		label := k.Label
		if k.Key == "x" {
			label += " (asks again; work and branches are kept)"
		}
		out = append(out, keyLine(vm.Key{Key: k.Key, Label: label}, ""))
	}
	if stopArmed {
		out = append(out, "", "Press x or Enter again to stop the run. Any other key cancels.")
	}
	out = append(out, "", "Bound to "+rev)
	if len(p.Tasks) > 0 {
		out = append(out, "", "AFFECTED TASKS")
		for _, t := range p.Tasks {
			out = append(out, strings.TrimSpace(t.Title+" · "+t.Progress+" · kept: "+t.Kept))
		}
	}
	if p.Room != "" {
		out = append(out, "Providers: "+p.Room)
	}
	if p.Run != "" {
		out = append(out, "Run: "+p.Run)
	}
	return out
}
