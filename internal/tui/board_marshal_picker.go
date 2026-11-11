package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// The Marshal picker (m) changes which model plans. Unlike the launch picker
// (board_picker.go) it is drawn mid-run as an overlay over the Board, the way
// help is: every installed provider with its live limit and whether it is busy,
// Enter to keep, ↑↓ or a click to change. Choosing a new Marshal never acts
// inside a step: it switches now when nothing is running, else it is queued and
// takes over when the step finishes (commands_marshal.go).

// pickerFor is the one overlay's configuration: the title it draws, the
// candidate rows (read once when it opened, never on the paint path), the
// provider marked "now" and the one marked "next", and what applies a choice.
// The Marshal picker (m) and the task-model picker (w, board_task_model.go)
// both fill it, so there is one overlay, one key handler and one mouse path.
// apply is called with the chosen provider, or "" when the picker is kept or
// cancelled (Esc, m); an out-of-limit row never reaches it.
type pickerFor struct {
	title   string
	cands   []pickerCand
	current string
	next    string
	apply   func(ctx context.Context, choice string)
}

// marshalPickerState is the mid-run picker's state: the configuration read once
// when it opened and the cursor over its rows.
type marshalPickerState struct {
	mu     sync.Mutex
	open   bool
	cursor int
	cfg    pickerFor
}

// marshalPickerView is a frame's copy of the picker, read under the lock once
// and painted without it.
type marshalPickerView struct {
	open    bool
	cursor  int
	title   string
	cands   []pickerCand
	current string
	next    string
}

// boardMarshalCandidates reads the installed Marshal providers' limits when the
// picker opens. Tests replace it to inject a fixed candidate list.
var boardMarshalCandidates = (*Workspace).marshalCandidates

// boardRunCommand runs a command the way a Board key does. Tests replace it to
// record the one switch command the picker and the queue drain issue. It is
// assigned in init so its default does not form a package initialization cycle
// with runCommand.
var boardRunCommand func(w *Workspace, ctx context.Context, cmd string)

func init() {
	boardRunCommand = func(w *Workspace, ctx context.Context, cmd string) { w.runCommand(ctx, cmd) }
}

// marshalDisplayName is a provider's name as the picker and every switch
// message show it; the person works with the Marshal, not the CLI's id.
func marshalDisplayName(provider string) string {
	switch provider {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "agy":
		return "Antigravity"
	case "opencode":
		return "OpenCode"
	}
	return provider
}

// currentMarshalProvider is the provider the Marshal runs now: the session's
// when it has one, else the project's saved default.
func (w *Workspace) currentMarshalProvider() string {
	m := w.marshalSession()
	m.mu.Lock()
	p := m.provider
	m.mu.Unlock()
	if p == "" {
		p = loadDefaultProvider(w.providerRoot())
	}
	return p
}

// queuedMarshalProvider is the provider waiting to take over when the current
// step finishes, or "" when none is queued.
func (w *Workspace) queuedMarshalProvider() string {
	m := w.marshalSession()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nextProvider
}

// clearQueuedMarshal cancels a queued switch; choosing the current Marshal does
// this.
func (w *Workspace) clearQueuedMarshal() {
	m := w.marshalSession()
	m.mu.Lock()
	m.nextProvider = ""
	m.mu.Unlock()
}

func (w *Workspace) marshalPickerOpen() bool {
	w.marshalPicker.mu.Lock()
	defer w.marshalPicker.mu.Unlock()
	return w.marshalPicker.open
}

func (w *Workspace) marshalPickerPaint() marshalPickerView {
	w.marshalPicker.mu.Lock()
	defer w.marshalPicker.mu.Unlock()
	cfg := w.marshalPicker.cfg
	return marshalPickerView{
		open:    w.marshalPicker.open,
		cursor:  w.marshalPicker.cursor,
		title:   cfg.title,
		cands:   append([]pickerCand(nil), cfg.cands...),
		current: cfg.current,
		next:    cfg.next,
	}
}

// openPickerFor opens the overlay with cfg over the Board, the cursor on the
// row marked "now" when it is a candidate. The Marshal picker and the
// task-model picker both call it.
func (w *Workspace) openPickerFor(cfg pickerFor) {
	cursor := 0
	if cfg.current != "" {
		for i, c := range cfg.cands {
			if c.provider == cfg.current {
				cursor = i
				break
			}
		}
	}
	w.marshalPicker.mu.Lock()
	w.marshalPicker.open, w.marshalPicker.cfg, w.marshalPicker.cursor = true, cfg, cursor
	w.marshalPicker.mu.Unlock()
	w.requestRepaint()
}

// openMarshalPicker reads the installed providers and opens the picker over the
// Board. With a single installed provider there is nothing to change, so it
// says so instead; with none it says so too.
func (w *Workspace) openMarshalPicker(ctx context.Context) {
	cands := boardMarshalCandidates(w, ctx)
	switch len(cands) {
	case 0:
		w.setOutput("No coding agent is installed.", false)
		return
	case 1:
		w.setOutput(marshalDisplayName(cands[0].provider)+" is the only coding agent installed, so it stays the Marshal.", false)
		return
	}
	w.openPickerFor(pickerFor{
		title:   " WHO PLANS? ",
		cands:   cands,
		current: w.currentMarshalProvider(),
		next:    w.queuedMarshalProvider(),
		apply:   w.applyMarshalChoice,
	})
}

func (w *Workspace) closeMarshalPicker() {
	w.marshalPicker.mu.Lock()
	w.marshalPicker.open, w.marshalPicker.cfg, w.marshalPicker.cursor = false, pickerFor{}, 0
	w.marshalPicker.mu.Unlock()
	w.requestRepaint()
}

// moveMarshalPicker moves the cursor and wraps at the ends.
func (w *Workspace) moveMarshalPicker(delta int) {
	w.marshalPicker.mu.Lock()
	if n := len(w.marshalPicker.cfg.cands); n > 0 {
		w.marshalPicker.cursor = ((w.marshalPicker.cursor+delta)%n + n) % n
	}
	w.marshalPicker.mu.Unlock()
	w.requestRepaint()
}

// handleMarshalPickerKey answers the Marshal picker while it is open: the
// arrows and j/k move the cursor, Enter applies the cursor row (or keeps on the
// current), 1..n pick-and-apply the nth row, Esc and m keep the current
// Marshal. Other single keys are swallowed so the picker stays modal; function
// keys and Ctrl chords pass on.
func (w *Workspace) handleMarshalPickerKey(ctx context.Context, ev KeyEvent) bool {
	w.marshalPicker.mu.Lock()
	open, n, cur := w.marshalPicker.open, len(w.marshalPicker.cfg.cands), w.marshalPicker.cursor
	w.marshalPicker.mu.Unlock()
	if !open {
		return false
	}
	switch {
	case ev.Type == KeyUp || ev.Type == KeyLeft || ev.Type == KeyRune && ev.Rune == 'k':
		w.moveMarshalPicker(-1)
		return true
	case ev.Type == KeyDown || ev.Type == KeyRight || ev.Type == KeyRune && ev.Rune == 'j':
		w.moveMarshalPicker(1)
		return true
	case ev.Type == KeyEsc, ev.Type == KeyRune && ev.Rune == 'm':
		w.keepPicker(ctx)
		return true
	case ev.Type == KeyEnter:
		w.applyPickerChoice(ctx, cur)
		return true
	case ev.Type == KeyRune && ev.Rune >= '1' && ev.Rune <= '9':
		if i := int(ev.Rune - '1'); i < n {
			w.applyPickerChoice(ctx, i)
		}
		return true
	}
	return ev.Type == KeyRune
}

// keepPicker closes the picker without choosing a row (Esc, m) and lets its
// apply settle: the Marshal picker says it stays and cancels a queued switch,
// the task-model picker does nothing.
func (w *Workspace) keepPicker(ctx context.Context) {
	w.marshalPicker.mu.Lock()
	apply := w.marshalPicker.cfg.apply
	w.marshalPicker.mu.Unlock()
	w.closeMarshalPicker()
	if apply != nil {
		apply(ctx, "")
	}
}

// applyMarshalChoice is the Marshal picker's apply: keeping (or choosing the
// current Marshal) cancels a queued switch and says it stays, any other
// provider switches through pickMarshal.
func (w *Workspace) applyMarshalChoice(ctx context.Context, choice string) {
	if choice == "" || choice == w.currentMarshalProvider() {
		name := marshalDisplayName(w.currentMarshalProvider())
		if name == "" {
			name = "the current one"
		}
		w.clearQueuedMarshal()
		w.setOutput("The Marshal stays "+name+".", false)
		return
	}
	w.pickMarshal(ctx, choice)
}

// applyPickerChoice answers the ith row: an out-of-limit row cannot be chosen
// (the picker stays open), any other row closes the picker and settles through
// the configured apply.
func (w *Workspace) applyPickerChoice(ctx context.Context, i int) {
	w.marshalPicker.mu.Lock()
	if !w.marshalPicker.open || i < 0 || i >= len(w.marshalPicker.cfg.cands) {
		w.marshalPicker.mu.Unlock()
		return
	}
	cand := w.marshalPicker.cfg.cands[i]
	apply := w.marshalPicker.cfg.apply
	w.marshalPicker.mu.Unlock()
	if cand.out {
		fig := "out of limit"
		if !cand.until.IsZero() {
			fig += " until " + cand.until.In(time.Now().Location()).Format("15:04")
		}
		w.setOutput(marshalDisplayName(cand.provider)+" is "+fig+".", false)
		return
	}
	w.closeMarshalPicker()
	if apply != nil {
		apply(ctx, cand.provider)
	}
}

// clickMarshalPicker applies the row a click reached, named by its provider.
func (w *Workspace) clickMarshalPicker(ctx context.Context, provider string) {
	w.marshalPicker.mu.Lock()
	idx := -1
	for i, c := range w.marshalPicker.cfg.cands {
		if c.provider == provider {
			idx = i
			break
		}
	}
	w.marshalPicker.mu.Unlock()
	if idx >= 0 {
		w.applyPickerChoice(ctx, idx)
	}
}

// pickMarshal applies a chosen provider: it refuses one that is not installed,
// queues the switch while a step is running, and otherwise runs the one switch
// command.
func (w *Workspace) pickMarshal(ctx context.Context, provider string) {
	name := marshalDisplayName(provider)
	if _, err := project.FindBinary(provider); err != nil {
		w.setOutput(name+" is not installed. Install it, then press m again.", false)
		return
	}
	m := w.marshalSession()
	m.mu.Lock()
	busy := m.busy
	if busy {
		m.nextProvider = provider
	}
	m.mu.Unlock()
	if busy {
		w.setOutput("The Marshal is in the middle of a step. "+name+" takes over when it finishes.", false)
		return
	}
	boardRunCommand(w, ctx, "/marshal model "+provider)
}

// applyQueuedMarshal drains a switch queued while a step was running. It runs
// after the step finishes (finishWithNativeTurn); if a new step has started it
// puts the choice back and waits for the next idle. An ordinary command still
// running would refuse the switch, so the choice also waits for that command
// to finish (runCommandFunc drains it again then).
func (w *Workspace) applyQueuedMarshal(ctx context.Context) {
	m := w.marshalSession()
	m.mu.Lock()
	p := m.nextProvider
	m.nextProvider = ""
	if (m.busy || w.commandBusy.Load()) && p != "" {
		m.nextProvider = p
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	if p == "" {
		return
	}
	boardRunCommand(w, ctx, "/marshal model "+p)
}

// marshalPickerOverlay draws the picker in the overlay rect r, registering a
// click region over each row (id: provider). It mirrors help's box so the two
// overlays read the same.
func (v boardVisual) marshalPickerOverlay(r layout.Rect, view marshalPickerView) []string {
	th := v.theme
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}
	w, h := r.W, r.H
	inner := max(1, w-4)
	arrows := "↑↓"
	if !v.unicode() {
		arrows = "Up/Down"
	}
	var body []string
	for i, c := range view.cands {
		marker := "  "
		if i == view.cursor {
			marker = "▸ "
			if !v.unicode() {
				marker = "> "
			}
		}
		glyph := ""
		if g := v.pickerGlyph(c.provider); g != "" {
			glyph = g + " "
		}
		mark := ""
		switch c.provider {
		case view.current:
			mark = "now"
		case view.next:
			mark = "next"
		}
		figure := ""
		colour := ""
		switch {
		case c.out:
			figure = "out of limit"
			if !c.until.IsZero() {
				figure += " until " + c.until.In(v.now.Location()).Format("15:04")
			}
			colour = "failed"
		case !c.known:
			figure = "unavailable"
		default:
			figure = fmt.Sprintf("%d%%", c.left)
		}
		row := marker + glyph + PadRight(marshalDisplayName(c.provider), 13) + " " + PadRight(mark, 4) + " " + figure
		if c.busy {
			row += "   busy"
		}
		coloured := row
		if colour != "" {
			coloured = v.fg(colour, row)
		}
		if i == view.cursor {
			coloured = v.bg("sel", coloured)
		}
		// The whole row is a click region that picks-and-applies it.
		v.hit(boardHit{row: r.Y + 1 + len(body), x0: r.X + 1, x1: r.X + 1 + inner, kind: hitPicker, id: c.provider})
		body = append(body, coloured)
	}

	title := view.title
	if title == "" {
		title = " WHO PLANS? "
	}
	hint := "Enter keep · " + arrows + " or click change · 1-" + strconv.Itoa(len(view.cands)) + " pick · Esc keep"
	out := []string{th.BoxTopLeft + th.BoxHoriz + v.bold(v.fg("white", title)) + strings.Repeat(th.BoxHoriz, max(0, w-3-VisibleLen(title))) + th.BoxTopRight}
	for _, l := range body {
		out = append(out, th.BoxVert+" "+PadCell(l, inner)+" "+th.BoxVert)
	}
	for len(out) < h-1 {
		out = append(out, th.BoxVert+strings.Repeat(" ", max(0, w-2))+th.BoxVert)
	}
	out = append(out, th.BoxBottomLeft+th.BoxHoriz+v.fg("dim", " "+hint+" ")+strings.Repeat(th.BoxHoriz, max(0, w-5-VisibleLen(hint)))+th.BoxBottomRight)
	for i := range out {
		out[i] = PadCell(out[i], w)
	}
	if !v.unicode() {
		out = hostASCII(boardASCIIGlyphs(out), w)
	}
	return out
}
