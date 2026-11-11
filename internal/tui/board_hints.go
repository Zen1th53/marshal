package tui

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// boardHints is the hint bar of one paint: registry IDs in display order,
// the lead the first matching state rule gives and then the tail, only the
// actions that can run now. footerSpans renders each as its first key and
// its Hint, a button for the action.
func (w *Workspace) boardHints(v boardVisual) []string {
	if w.launchBlocked() {
		// The Blocked screen takes only r, q and help.
		return w.availableHints([]string{"launch.retry", "help"})
	}
	var lead []string
	// Rules 1-3 hold the keyboard: any other key cancels the confirmation,
	// moves in the picker or is typed, so the tail's keys do not act there.
	tail := true
	inView := v.hostView || v.memView || v.provView || v.verView || v.secView || v.planView || v.archView
	switch {
	case w.boardConfirmArmed(): // 1: an armed confirmation
		lead, tail = []string{"confirm.enter", "confirm.cancel"}, false
	case w.pickerOpen() || w.marshalPickerOpen(): // 2: a picker
		lead, tail = []string{"picker.use", "picker.choose", "picker.keep"}, false
	case w.composing(): // 3: typing
		lead, tail = []string{"typing.send", "typing.cancel"}, false
	case v.empty: // 4: no run on screen
		lead = []string{"say.build", "firstuse.chat", "firstuse.verify", "marshal.pick"}
	case waitingGates(v.gates) > 0: // 5: a gate waits
		lead = []string{"gate.reply"}
	case v.guard != nil: // 6: the guard's alert
		lead = []string{"guard.undo", "guard.keep"}
	case w.stuckUnderKeyboard(): // 7: a stuck task
		lead = []string{"task.retry", "task.model", "task.inspect"}
	case w.waitingUnderKeyboard(): // 8: a queued or sent-back task
		lead = []string{"task.model", "task.inspect"}
	case !inView && v.focus == layout.Inbox && len(v.board.Inbox) > 0: // 9: the inbox
		lead = []string{"inbox.allow", "inbox.deny", "inbox.show"}
	case inView: // 10: a body view's own keys
		lead = v.viewHints()
	default: // 11
		lead = []string{"board.move", "task.inspect", "marshal.say"}
	}
	out := lead
	if tail {
		out = append(slices.Clone(lead), "palette")
		if !slices.Contains(lead, "marshal.pick") {
			out = append(out, "marshal.pick")
		}
		if v.docked || w.nativeChatShown() {
			out = append(out, "marshal.chat")
		}
		out = append(out, "help")
	}
	return w.availableHints(out)
}

// viewHints is rule 10: the open body view's keys, the ones its footer has
// always shown.
func (v boardVisual) viewHints() []string {
	switch {
	case v.viewNoRun && (v.verView || v.secView || v.planView):
		return []string{"view.leave", "marshal.say"}
	case v.hostView:
		return []string{"view.scroll", "view.leave"}
	case v.memView:
		return []string{"view.select", "memory.keep", "memory.reject", "memory.evidence", "memory.page", "view.leave"}
	case v.provView:
		return []string{"view.select", "providers.verify", "view.leave"}
	case v.verView:
		return []string{"view.select", "verify.return", "view.leave"}
	case v.secView:
		return []string{"view.select", "security.allow", "security.deny", "view.leave"}
	case v.planView:
		return []string{"view.select", "plan.open", "view.leave"}
	case v.archView:
		return []string{"arch.open", "view.leave"}
	}
	return nil
}

// availableHints keeps the ids whose action can run now, each once.
func (w *Workspace) availableHints(ids []string) []string {
	var out []string
	for _, id := range ids {
		k, ok := boardAction(id)
		if !ok || slices.Contains(out, id) {
			continue
		}
		if run, _ := k.available(w); run {
			out = append(out, id)
		}
	}
	return out
}

// hintHoldsKeyboard reports whether id is a lead of rules 1-3: a hint for the
// confirmation, picker or composer that has the keyboard. A click on one
// reaches it even while that keyboard owner swallows other clicks, and the
// footer shows these beside the status line.
func hintHoldsKeyboard(id string) bool {
	return strings.HasPrefix(id, "confirm.") || strings.HasPrefix(id, "picker.") || strings.HasPrefix(id, "typing.")
}

// hintKey is how the hint bar writes a row's key: the first of its Keys,
// "⏎" for Enter ("Enter" in ASCII), "esc", "^X" for a Ctrl key, and the keys
// of one alternative joined by "/" ("j k" is "j/k").
func (v boardVisual) hintKey(keys string) string {
	first, _, _ := strings.Cut(keys, "  ")
	switch {
	case first == "Enter":
		if v.unicode() {
			return "⏎"
		}
		return "Enter"
	case first == "Esc":
		return "esc"
	case first == "↑↓" && !v.unicode():
		return "Up/Dn"
	case strings.HasPrefix(first, "Ctrl+"):
		return "^" + strings.TrimPrefix(first, "Ctrl+")
	}
	return strings.ReplaceAll(first, " ", "/")
}

// boardConfirmArmed reports whether a confirmation waits for Enter: a stuck
// task's retry, the guard's undo, an owner gate's reply, a first-use verify
// or an inbox decision.
func (w *Workspace) boardConfirmArmed() bool {
	g := &w.guard
	g.mu.Lock()
	guard := g.alert != nil && g.armed
	g.mu.Unlock()
	return w.boardUI.retryArmed != "" || guard || w.gateArmed() != "" || w.firstUseArmed() != "" || w.inboxView.Confirming()
}

// stuckUnderKeyboard is rule 7: the task under the keyboard is stuck.
func (w *Workspace) stuckUnderKeyboard() bool {
	_, ok := stuckTaskUnderKeyboard(w)
	return ok
}

// waitingUnderKeyboard is rule 8: the task under the keyboard has not
// started or was sent back for rework.
func (w *Workspace) waitingUnderKeyboard() bool {
	id, ok := taskUnderKeyboard(w)
	if !ok {
		return false
	}
	row, _ := taskRow(w.marshalPanel().Board, id)
	return row.State == boardmodel.TaskQueued || row.State == boardmodel.TaskRework
}

// firstUseBoard reports whether the first-use card is on screen: the Board
// with no run, not blocked.
func (w *Workspace) firstUseBoard() bool {
	return w.boardVisible() && w.marshalPanel() == nil && !w.launchBlocked()
}

// guardAlerting reports whether the plan-only guard's alert is up; armed
// reports whether its undo waits for Enter.
func (w *Workspace) guardAlerting() (alert, armed bool) {
	g := &w.guard
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.alert != nil, g.armed
}

// gateWaiting reports whether an owner gate's reply is still to send.
func (w *Workspace) gateWaiting() bool {
	return waitingGates(w.ownerGates(w.marshalPanel())) > 0
}

// pressing is a Run that presses ev through the path a keypress takes, so a
// click on the hint does exactly what its key does.
func pressing(ev KeyEvent) func(*Workspace, context.Context) {
	return func(w *Workspace, ctx context.Context) { w.handleTerminalKey(ctx, ev) }
}

// beginInbox is a Run that opens the inbox's own confirmation for key on the
// selected request, as the key pressed in the inbox does. It never decides:
// only Enter in that confirmation does.
func beginInbox(key rune) func(*Workspace, context.Context) {
	return func(w *Workspace, _ context.Context) {
		items := w.boardInbox()
		if len(items) == 0 {
			return
		}
		w.closeBodyViews()
		w.boardHelp.Store(false)
		w.boardUI.inspect = ""
		w.boardUI.focus = layout.Inbox
		w.inboxView.Update(items)
		w.inboxView.Begin(key)
	}
}

// boardHintKeymap is the hint bar's own rows: one per key and label a state
// rule shows that no key row of boardKeymap writes that way. They are
// listed only, so help, the palette and the keymap tests never see them;
// boardAction finds them, and a click runs their Run. It is filled in init:
// a Run reaches handleTerminalKey, which reaches boardAction, so a literal
// would be an initialization cycle.
var boardHintKeymap []boardKey

func init() {
	confirmWhy := reason("Nothing is waiting for Enter.")
	pickerOpen := func(w *Workspace) bool { return w.pickerOpen() || w.marshalPickerOpen() }
	composing := func(w *Workspace) bool { return w.composing() }
	firstUse := func(w *Workspace) bool { return w.firstUseBoard() }
	firstUseWhy := reason("There is a run on screen.")
	inboxWaiting := func(w *Workspace) bool { return len(w.boardInbox()) > 0 }
	inboxWhy := reason("The inbox has nothing to decide.")
	anyView := func(w *Workspace) bool { return w.anyBodyView() }
	viewWhy := reason("No view is open.")
	mem := func(w *Workspace) bool { return w.memView.Load() }
	memWhy := reason("Open the Memory view first.")
	sec := func(w *Workspace) bool { return w.secView.Load() }
	secWhy := reason("Open the Security view first.")
	row := func(id, keys, hint string, when func(*Workspace) bool, why func(*Workspace) string, run func(*Workspace, context.Context)) boardKey {
		return boardKey{Group: "HINT", Keys: keys, Hint: hint, Listed: true, Class: KeyOpen, ID: id, When: when, Why: why, Run: run}
	}
	boardHintKeymap = []boardKey{
		// Rule 1. Enter decides, so a click on it only says so: a click
		// never decides. Esc cancels, the key's own path.
		row("confirm.enter", "Enter", "confirm", (*Workspace).boardConfirmArmed, confirmWhy, func(w *Workspace, _ context.Context) {
			w.setOutput("Press Enter to confirm; a click never confirms.", false)
		}),
		row("confirm.cancel", "Esc", "cancel", (*Workspace).boardConfirmArmed, confirmWhy, pressing(KeyEvent{Type: KeyEsc})),
		// Rule 2. The launch picker must be answered, so only the Marshal
		// and task pickers keep on Esc.
		row("picker.use", "Enter", "use", pickerOpen, reason("No picker is open."), pressing(KeyEvent{Type: KeyEnter})),
		row("picker.choose", "↑↓", "choose", pickerOpen, reason("No picker is open."), pressing(KeyEvent{Type: KeyDown})),
		row("picker.keep", "Esc", "keep", (*Workspace).marshalPickerOpen, reason("No picker that keeps on Esc is open."), pressing(KeyEvent{Type: KeyEsc})),
		// Rule 3.
		row("typing.send", "Enter", "send", composing, reason("Nothing is being typed."), pressing(KeyEvent{Type: KeyEnter})),
		row("typing.cancel", "Esc", "cancel", composing, reason("Nothing is being typed."), pressing(KeyEvent{Type: KeyEsc})),
		// Rule 4: the first-use card's own keys.
		row("say.build", "s", "say what to build", firstUse, firstUseWhy, pressing(KeyEvent{Type: KeyRune, Rune: 's'})),
		row("firstuse.chat", "Enter", "chat", firstUse, firstUseWhy, func(w *Workspace, ctx context.Context) { w.focusChatFromBoard(ctx) }),
		row("firstuse.verify", "v", "verify", func(w *Workspace) bool {
			return w.firstUseBoard() && nextUnverified(w.firstUseChips(time.Now())) != ""
		}, reason("Every installed provider is verified or busy."), func(w *Workspace, _ context.Context) {
			w.armFirstUseVerify(nextUnverified(w.firstUseChips(time.Now())))
		}),
		// Rule 5: y arms the first unsent reply; Enter sends it.
		row("gate.reply", "y", "reply", (*Workspace).gateWaiting, reason("Nothing is waiting for your reply."), pressing(KeyEvent{Type: KeyRune, Rune: 'y'})),
		// Rule 6: undo opens its confirmation; keep dismisses the alert, as
		// the alert's own K button does.
		row("guard.undo", "u", "undo", func(w *Workspace) bool { alert, armed := w.guardAlerting(); return alert && !armed }, reason("No changed files wait for undo."), func(w *Workspace, _ context.Context) { w.guardArm() }),
		row("guard.keep", "K", "keep", func(w *Workspace) bool { alert, _ := w.guardAlerting(); return alert }, reason("No changed files wait for a decision."), func(w *Workspace, _ context.Context) { w.clickGuard('K') }),
		// Rules 7, 8 and 11: Enter on the selection.
		row("task.inspect", "Enter", "inspect", func(w *Workspace) bool { return w.boardUI.task != "" || w.boardUI.lane != "" }, reason("Select a task or a lane first."), pressing(KeyEvent{Type: KeyEnter})),
		// Rule 9: each opens the inbox's confirmation, never decides.
		row("inbox.allow", "a", "allow", inboxWaiting, inboxWhy, beginInbox('a')),
		row("inbox.deny", "d", "deny", inboxWaiting, inboxWhy, beginInbox('d')),
		row("inbox.show", "i", "reason", inboxWaiting, inboxWhy, beginInbox('i')),
		// Rule 10: a body view's keys, each its key's own path.
		row("view.scroll", "j k", "scroll", anyView, viewWhy, nil),
		row("view.select", "j k", "select", anyView, viewWhy, nil),
		row("view.leave", "Esc", "board", anyView, viewWhy, pressing(KeyEvent{Type: KeyEsc})),
		row("memory.keep", "a", "keep", mem, memWhy, pressing(KeyEvent{Type: KeyRune, Rune: 'a'})),
		row("memory.reject", "r", "reject", mem, memWhy, pressing(KeyEvent{Type: KeyRune, Rune: 'r'})),
		row("memory.evidence", "i", "evidence", mem, memWhy, pressing(KeyEvent{Type: KeyRune, Rune: 'i'})),
		row("memory.page", "PgDn", "scroll", mem, memWhy, pressing(KeyEvent{Type: KeyPgDn})),
		row("security.allow", "a", "allow for run", sec, secWhy, pressing(KeyEvent{Type: KeyRune, Rune: 'a'})),
		row("security.deny", "d", "deny", sec, secWhy, pressing(KeyEvent{Type: KeyRune, Rune: 'd'})),
		row("plan.open", "Enter", "inspect", func(w *Workspace) bool { return w.planView.Load() }, reason("Open the Plan view first."), pressing(KeyEvent{Type: KeyEnter})),
		// The Blocked screen's r and its [r] button.
		row("launch.retry", "r", "retry", (*Workspace).launchBlocked, reason("Nothing is blocked."), pressing(KeyEvent{Type: KeyRune, Rune: 'r'})),
	}
	// Two key rows whose click must take the key's own path, which the
	// palette's does not: F11's chat switch and the palette's Ctrl+K.
	for i := range boardKeymap {
		switch boardKeymap[i].ID {
		case "marshal.chat":
			boardKeymap[i].Run = pressing(KeyEvent{Type: KeyF11})
		case "palette":
			boardKeymap[i].Run = pressing(KeyEvent{Type: KeyCtrlK})
		}
	}
}
