package tui

import (
	"context"
	"slices"
	"strconv"
	"sync"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// The Board's mouse is navigation and the way into a confirmation, never a
// decision: a click selects, focuses, inspects or opens a view, and a click
// on an inbox action opens the same confirmation its key opens. Only the
// confirmation's own keys decide.

// boardHitKind is what a click on a region reaches.
type boardHitKind string

const (
	hitTask  boardHitKind = "task"  // a task row: select, then inspect
	hitLane  boardHitKind = "lane"  // a lane row or chip: focus, then show
	hitChip  boardHitKind = "chip"  // a header chip: its view
	hitInbox boardHitKind = "inbox" // an inbox row, or one of its action buttons
	// hitInspect is a button of the inspector's action row: the key it
	// names, pressed in the inspector on that task.
	hitInspect boardHitKind = "inspect"
	hitStop    boardHitKind = "stop"
	// hitVerify is a provider on the first-use card: a click arms its
	// verify, which Enter runs.
	hitVerify boardHitKind = "verify"
	// hitNote is a background message in Activity: a click shows it
	// whole.
	hitNote boardHitKind = "note"
	// hitGate is an owner gate in the inbox (board_gates.go): its reply
	// button arms the reply, which Enter or a second click sends; its
	// details button opens the view the gate is about.
	hitGate boardHitKind = "gate"
	// hitGuard is the plan-only guard's entry in the inbox
	// (board_guard.go): undo arms, a second click or Enter runs it; keep
	// dismisses it.
	hitGuard boardHitKind = "guard"
	// hitAsk is a choice on an ask card (board_ask.go): a click sends that
	// choice's label to the Marshal chat and closes the card, as its key
	// does. There is no arm step.
	hitAsk boardHitKind = "ask"
	// hitRequirements is a region of the requirements card
	// (board_requirements.go): the Enter or e button, or a line row; a click
	// confirms, starts an edit, or edits that line, as the keys do.
	hitRequirements boardHitKind = "requirements"
	// hitQuota is a button of the all-providers-at-limit card
	// (board_quota.go): r arms auto-resume, s stops here.
	hitQuota boardHitKind = "quota"
	// hitChange is an action on a change card (board_change.go): a click
	// applies the change or keeps the current goal, as its key does.
	hitChange boardHitKind = "change"
	// hitPicker is a candidate on the launch Marshal picker (board_picker.go):
	// a click picks-and-confirms it. id is its index.
	hitPicker boardHitKind = "picker"
	// hitNext is a button on the "what next?" card (board_next.go): a click
	// does the same as its key — n starts the next goal, d opens the Verify
	// view, q quits.
	hitNext boardHitKind = "next"
	// hitAction is a button a painter draws for a registry action
	// (board_keymap.go): its id is the action's ID, and a click runs that
	// action the way its key would.
	hitAction boardHitKind = "action"
	// hitPaletteRow is a visible row of the command palette (palette.go):
	// its id is the row's index in cp.filtered. A first click selects it, a
	// click on the selected row runs it exactly as Enter does.
	hitPaletteRow boardHitKind = "palette"
)

// boardHit is one clickable region of the last Board paint: frame row and
// the cells [x0, x1), both from zero.
type boardHit struct {
	row, x0, x1 int
	kind        boardHitKind
	id          string // task id, lane worker, chip name, inbox request key
	action      rune   // inbox: the key the button stands for, 0 for the row
}

// boardHitMap holds the regions of the frame on screen. The paint replaces
// it whole, so a click never reaches a row the screen no longer shows.
type boardHitMap struct {
	mu   sync.Mutex
	hits []boardHit
}

func (m *boardHitMap) set(hits []boardHit) {
	m.mu.Lock()
	m.hits = hits
	m.mu.Unlock()
}

// at is the region under the cell; a button wins over the row it sits on.
func (m *boardHitMap) at(row, col int) (boardHit, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found boardHit
	ok := false
	for _, h := range m.hits {
		if h.row == row && col >= h.x0 && col < h.x1 {
			if !ok || h.action != 0 {
				found, ok = h, true
			}
		}
	}
	return found, ok
}

// hit records a region on the paint in progress.
func (v boardVisual) hit(h boardHit) {
	if v.hits != nil && h.x1 > h.x0 {
		*v.hits = append(*v.hits, h)
	}
}

// unhit drops the regions recorded on a row a later line replaced.
func (v boardVisual) unhit(row int) {
	if v.hits != nil {
		*v.hits = slices.DeleteFunc(*v.hits, func(h boardHit) bool { return h.row == row })
	}
}

// boardMouseWanted reports whether the Board reads the mouse: while it is
// on screen with nothing over it that takes the mouse itself. The palette is
// drawn over the Board and its rows are clickable, so it does not turn the
// mouse off; the diff viewer owns the whole screen and does.
func (w *Workspace) boardMouseWanted() bool {
	return w.boardVisible() && !w.diffViewer.IsOpen()
}

// handleBoardMouse is a click on the Board. It returns false for anything
// that is not the Board's: a mouse report with the Board hidden, or with
// the diff viewer over it.
func (w *Workspace) handleBoardMouse(ctx context.Context, event KeyEvent) bool {
	if event.Type != KeyMouse || !w.boardVisible() || w.diffViewer.IsOpen() || w.completionOpen {
		return false
	}
	// The palette is drawn over the Board: it takes the wheel and the click
	// itself, so it is handled before the Board's own "left press only" rule.
	if w.palette.IsOpen() {
		return w.handlePaletteMouse(ctx, event)
	}
	// Only a left press acts; releases, other buttons and the wheel are
	// the Board's to ignore, so they never reach the composer.
	if event.MouseRelease || event.MouseButton != 0 {
		return true
	}
	// A click during the opening animation (P2-03) ends it, as any key does.
	if w.openingAnimating() {
		w.endOpening()
		return true
	}
	if w.boardHelp.Load() {
		// A click closes help; one that lands on a help line naming a runnable
		// key also runs that action, as pressing the key would. A KeyConfirm
		// key only opens its confirmation, so the mouse still never decides.
		w.boardHelp.Store(false)
		if h, ok := w.boardHits.at(event.MouseRow-1, event.MouseColumn-1); ok && h.kind == hitAction {
			w.runBoardAction(ctx, h.id)
		}
		return true
	}
	h, ok := w.boardHits.at(event.MouseRow-1, event.MouseColumn-1)
	// The hint bar's lead for the confirmation, picker or composer that has
	// the keyboard is that owner's own key (board_hints.go); it acts even
	// where every other click is held back below.
	if ok && h.kind == hitAction && hintHoldsKeyboard(h.id) {
		w.runBoardAction(ctx, h.id)
		return true
	}
	// A confirmation in progress, or a command being typed, keeps the
	// keyboard: a click must not move the decision under it.
	if w.inboxView.Confirming() || w.composing() {
		return true
	}
	// The mid-run Marshal picker is an overlay: a click on a row picks and
	// applies it, a click anywhere else closes it (choosing is reversible and
	// never acts mid-step, so a click may decide here).
	if w.marshalPickerOpen() {
		if ok && h.kind == hitPicker {
			w.clickMarshalPicker(ctx, h.id)
		} else {
			w.closeMarshalPicker()
		}
		return true
	}
	if !ok {
		return true
	}
	// The launch picker is modal: only a click on one of its candidates acts.
	if w.pickerOpen() {
		if h.kind == hitPicker {
			if i, err := strconv.Atoi(h.id); err == nil {
				w.confirmPicker(i)
			}
		}
		return true
	}
	w.mu.RLock()
	p := w.state.Marshal
	w.boardUI.ack, w.boardUI.ackSeq = w.state.LastOutput, w.outputSeq.Load()
	w.mu.RUnlock()
	if p == nil {
		p = &MarshalPanel{}
	}
	ui := &w.boardUI
	items := w.boardInbox()
	w.inboxView.Update(items)
	focus := ui.pane(len(items) > 0)
	switch h.kind {
	case hitTask:
		if !slices.ContainsFunc(p.Board.Tasks, func(t boardmodel.TaskRow) bool { return t.ID == h.id }) {
			return true
		}
		if focus == layout.Tasks && ui.task == h.id {
			// A click on the selected task inspects it, as Enter does.
			ui.inspect = h.id
		}
		ui.focus, ui.task = layout.Tasks, h.id
	case hitLane:
		if !slices.ContainsFunc(p.Board.Lanes, func(l boardmodel.Lane) bool { return l.Worker == h.id }) {
			return true
		}
		again := focus == layout.Lanes && ui.lane == h.id
		ui.focus, ui.lane = layout.Lanes, h.id
		if again {
			// A click on the focused lane shows its worker, as Enter does.
			w.enterLane(p.Board)
		}
	case hitChip:
		w.openChip(h.id)
	case hitStop:
		w.boardUI.focus = layout.Inbox
		w.handleBoardKey(KeyEvent{Type: KeyEnter})
	case hitVerify:
		w.armFirstUseVerify(h.id)
	case hitNote:
		if id, err := strconv.Atoi(h.id); err == nil {
			w.openNote(id)
		}
	case hitGate:
		w.clickGate(h.id, h.action)
	case hitGuard:
		w.clickGuard(h.action)
	case hitAsk:
		if i, err := strconv.Atoi(h.id); err == nil {
			w.answerAsk(i)
		}
	case hitRequirements:
		w.clickRequirements(h.id)
	case hitQuota:
		w.clickQuota(h.action)
	case hitChange:
		w.clickChange(h.id)
	case hitNext:
		w.clickNext(h.id)
	case hitInspect:
		// The same paths the inspector's keys take: a decision button
		// opens the inbox's confirmation, never decides; o shows the pane.
		if h.action == 'o' {
			w.inspectorWorkerPane(p.Board, h.id)
		} else {
			w.inspectorDecision(h.id, h.action)
		}
	case hitAction:
		// A registry button runs its action the one way its key does.
		w.runBoardAction(ctx, h.id)
	case hitInbox:
		want := slices.IndexFunc(items, func(it boardmodel.InboxItem) bool { return it.Request.Key() == h.id })
		if want < 0 {
			return true
		}
		cur := slices.IndexFunc(items, func(it boardmodel.InboxItem) bool { return it.Request.Key() == w.inboxView.Selected() })
		if cur >= 0 && cur != want {
			w.inboxView.Move(want - cur)
		}
		ui.focus = layout.Inbox
		if h.action != 0 {
			// The button opens the confirmation; Enter in it decides.
			w.inboxView.Begin(h.action)
		}
	}
	return true
}

// handlePaletteMouse is a mouse report while the command palette is open over
// the Board. The wheel moves the selection as ↑/↓ do; a first left click on a
// row selects it, a click on the already-selected row runs it exactly as
// Enter, and a left click outside the box closes the palette.
func (w *Workspace) handlePaletteMouse(ctx context.Context, event KeyEvent) bool {
	// The wheel moves the selection by one, like ↑/↓.
	switch event.MouseButton {
	case 64:
		w.palette.HandleKey(KeyEvent{Type: KeyUp})
		return true
	case 65:
		w.palette.HandleKey(KeyEvent{Type: KeyDown})
		return true
	}
	// Beyond the wheel only a left press acts; releases and other buttons
	// are ignored so they never reach the composer.
	if event.MouseRelease || event.MouseButton != 0 {
		return true
	}
	h, ok := w.boardHits.at(event.MouseRow-1, event.MouseColumn-1)
	if !ok || h.kind != hitPaletteRow {
		// A click outside the box closes the palette, as Esc does.
		w.palette.Close()
		return true
	}
	i, err := strconv.Atoi(h.id)
	if err != nil {
		return true
	}
	if w.palette.Selected() == i {
		// A click on the selected row runs it the one way Enter does.
		if action, run := w.palette.HandleKey(KeyEvent{Type: KeyEnter}); run && action != nil {
			w.runPaletteAction(ctx, *action)
		}
		return true
	}
	// A first click only selects the row; a second click on it runs it.
	w.palette.Select(i)
	return true
}

// boardChipViews maps the header chips that stand for a body view to the
// key that opens it.
var boardChipViews = map[string]rune{"health": '4', "host": 'h'}

// openChip opens what a header chip stands for: its view, or the Board with
// the pane it counts focused.
func (w *Workspace) openChip(name string) {
	ui := &w.boardUI
	if r, ok := boardChipViews[name]; ok {
		ui.inspect = ""
		if !w.switchBodyView(KeyEvent{Type: KeyRune, Rune: r}) {
			w.setOutput("That view has nothing to read in this session.", false)
		}
		return
	}
	var pane layout.PaneID
	switch name {
	case "inbox":
		pane = layout.Inbox
	case "lanes":
		pane = layout.Lanes
	case "run", "state", "done", "brand":
		pane = layout.Tasks
	default:
		return
	}
	w.closeBodyViews()
	ui.inspect = ""
	ui.focus = pane
}

// boardClickableChips are the header chips a click acts on; the rest
// (budget, clock) are read-only and record no region.
func boardChipClickable(name string) bool {
	switch name {
	case "inbox", "lanes", "run", "state", "done", "brand":
		return true
	}
	_, ok := boardChipViews[name]
	return ok
}
