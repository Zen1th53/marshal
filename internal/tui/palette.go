package tui

import (
	"fmt"
	"slices"
	"strings"
)

// PaletteAction represents a discoverable action in the Command Palette.
type PaletteAction struct {
	ID          string // e.g. "goal.edit"
	Category    string // e.g. "GOAL", "TEAM", "POLICY", "SYSTEM"
	Title       string // e.g. "Edit Goal & Objectives"
	Description string // e.g. "Modify active goal objective and constraints"
	Command     string // e.g. "/goal edit"
	Shortcut    string // e.g. "g" or empty
	Disabled    bool
	Reason      string // reason if disabled
	// Key is the Board key the entry sends instead of a command; View
	// marks an entry that opens one of the Board's views with it.
	Key  *KeyEvent
	View bool
	// Pane is the Board pane or view the entry acts in ("inbox", "Memory");
	// empty for an entry that acts anywhere. The palette ranks the focused
	// pane's entries first.
	Pane string
	// Inbox is the key of the pending request an inbox entry decides.
	Inbox string
}

// PaletteContext is what the palette ranks by while it is open: the pane
// or view the focus is in, the actions of the selection (the selected
// task's or inbox item's pending request), and the other actions the
// moment offers (the rest of the inbox). It is read once as the palette
// opens, so the order does not move under the operator, and for a given
// state it is always the same order.
type PaletteContext struct {
	Pane      string
	Selection []PaletteAction
	Others    []PaletteAction
}

// Context ranks, highest first.
const (
	rankSelection = 3
	rankPane      = 2
	rankOther     = 1
)

// CommandPalette manages the actions palette modal (Space / Ctrl+K).
type CommandPalette struct {
	theme    *Theme
	active   bool
	query    []rune
	actions  []PaletteAction
	filtered []PaletteAction
	selected int
	// context reads the PaletteContext as the palette opens; ctx is the
	// one read.
	context func() PaletteContext
	ctx     PaletteContext
	// refresh, when set, rebuilds the full list of actions each time the
	// palette opens, so a row's availability is current.
	refresh func() []PaletteAction
}

// NewCommandPalette creates a new CommandPalette with actions.
func NewCommandPalette(th *Theme, actions []PaletteAction) *CommandPalette {
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}
	cp := &CommandPalette{
		theme:   th,
		actions: actions,
	}
	cp.filter()
	return cp
}

// SetActions updates the full list of actions.
func (cp *CommandPalette) SetActions(actions []PaletteAction) {
	cp.actions = actions
	cp.filter()
}

// IsOpen returns true if the palette is currently visible.
func (cp *CommandPalette) IsOpen() bool {
	return cp.active
}

// Selected is the index of the highlighted row in cp.filtered.
func (cp *CommandPalette) Selected() int {
	return cp.selected
}

// Select moves the highlight to row i of cp.filtered, clamped to the rows
// that exist. A mouse click selects the row it reaches with it.
func (cp *CommandPalette) Select(i int) {
	switch {
	case i < 0:
		cp.selected = 0
	case i >= len(cp.filtered):
		cp.selected = max(0, len(cp.filtered)-1)
	default:
		cp.selected = i
	}
}

// Layout is the palette box's geometry for a width-by-height overlay: the
// box's width in cells and the half-open range [start, end) of cp.filtered
// the box shows. Render and the mouse hit code both read it, so a row's
// screen position is computed one way.
func (cp *CommandPalette) Layout(width, height int) (boxWidth, start, end int) {
	boxWidth = width - 8
	if boxWidth > 76 {
		boxWidth = 76
	}
	if boxWidth < 40 {
		boxWidth = width
	}
	maxItems := 10
	if height > 0 && maxItems > height-6 {
		maxItems = height - 6
	}
	if maxItems < 3 {
		maxItems = 3
	}
	if cp.selected >= maxItems {
		start = cp.selected - maxItems + 1
	}
	end = start + maxItems
	if end > len(cp.filtered) {
		end = len(cp.filtered)
	}
	return boxWidth, start, end
}

// Open activates the command palette and resets query.
func (cp *CommandPalette) Open() {
	cp.active = true
	cp.query = make([]rune, 0)
	cp.selected = 0
	cp.ctx = PaletteContext{}
	if cp.refresh != nil {
		cp.actions = cp.refresh()
	}
	if cp.context != nil {
		cp.ctx = cp.context()
	}
	cp.filter()
}

// Close dismisses the command palette.
func (cp *CommandPalette) Close() {
	cp.active = false
	cp.query = nil
	cp.selected = 0
	cp.ctx = PaletteContext{}
	cp.filter()
}

// Toggle toggles the command palette.
func (cp *CommandPalette) Toggle() {
	if cp.active {
		cp.Close()
	} else {
		cp.Open()
	}
}

// HandleKey processes input when the palette is active.
// Returns (selectedAction, executed bool)
func (cp *CommandPalette) HandleKey(k KeyEvent) (*PaletteAction, bool) {
	if !cp.active {
		return nil, false
	}

	switch k.Type {
	case KeyEsc, KeyCtrlK:
		cp.Close()
		return nil, false

	case KeyEnter:
		if len(cp.filtered) > 0 && cp.selected >= 0 && cp.selected < len(cp.filtered) {
			action := cp.filtered[cp.selected]
			if !action.Disabled {
				cp.Close()
				return &action, true
			}
		}
		return nil, false

	case KeyUp:
		if cp.selected > 0 {
			cp.selected--
		} else if len(cp.filtered) > 0 {
			cp.selected = len(cp.filtered) - 1
		}
		return nil, false

	case KeyDown:
		if cp.selected < len(cp.filtered)-1 {
			cp.selected++
		} else {
			cp.selected = 0
		}
		return nil, false

	case KeyBackspace:
		if len(cp.query) > 0 {
			cp.query = cp.query[:len(cp.query)-1]
			cp.filter()
		}
		return nil, false

	case KeyRune:
		cp.query = append(cp.query, k.Rune)
		cp.filter()
		return nil, false
	}

	return nil, false
}

// filter orders the entries the query matches. With no query that is the
// context's order: the selection's actions, then the focused pane's
// entries, then the context's other actions, then the rest as listed.
// A query ranks how well an entry matches first: a key typed as it is
// (a, A, d) picks the entries it is the key of, then a word of a title
// the query starts, then the query anywhere; ties keep the context order
// and, typed, put the entries that can run before those that cannot.
// The sort is stable, so one state always gives one order.
func (cp *CommandPalette) filter() {
	raw := strings.TrimSpace(string(cp.query))
	query := strings.ToLower(raw)
	type ranked struct {
		a           PaletteAction
		match, rank int
	}
	var all []ranked
	add := func(as []PaletteAction, rank int) {
		for _, a := range as {
			r := rank
			if rank == 0 && a.Pane != "" && a.Pane == cp.ctx.Pane {
				r = rankPane
			}
			all = append(all, ranked{a: a, rank: r})
		}
	}
	add(cp.ctx.Selection, rankSelection)
	add(cp.ctx.Others, rankOther)
	add(cp.actions, 0)
	cp.filtered = cp.filtered[:0]
	kept := all[:0]
	for _, r := range all {
		r.match = paletteMatch(r.a, raw, query)
		if r.match > 0 || query == "" {
			kept = append(kept, r)
		}
	}
	slices.SortStableFunc(kept, func(x, y ranked) int {
		if x.match != y.match {
			return y.match - x.match
		}
		if x.rank != y.rank {
			return y.rank - x.rank
		}
		if query != "" && x.a.Disabled != y.a.Disabled {
			if x.a.Disabled {
				return 1
			}
			return -1
		}
		return 0
	})
	for _, r := range kept {
		cp.filtered = append(cp.filtered, r.a)
	}

	if cp.selected >= len(cp.filtered) {
		if len(cp.filtered) > 0 {
			cp.selected = len(cp.filtered) - 1
		} else {
			cp.selected = 0
		}
	}
}

// paletteMatch is how well an entry matches the query, raw as typed and
// lowered: 3 for a key the entry is bound to, 2 for a title word the
// query starts, 1 for the query anywhere in the entry, 0 for no match.
func paletteMatch(a PaletteAction, raw, query string) int {
	if query == "" {
		return 0
	}
	if slices.Contains(strings.Fields(a.Shortcut), raw) {
		return 3
	}
	for _, f := range strings.FieldsFunc(strings.ToLower(a.Title), func(r rune) bool {
		return r == ' ' || r == ':' || r == '·' || r == '(' || r == ')' || r == ','
	}) {
		if strings.HasPrefix(f, query) {
			return 2
		}
	}
	target := strings.ToLower(fmt.Sprintf("%s %s %s %s %s", a.Category, a.Title, a.Description, a.Command, a.Shortcut))
	if strings.Contains(target, query) {
		return 1
	}
	return 0
}

// Render renders the Command Palette box for display in the terminal.
// width and height constrain the maximum dimensions.
func (cp *CommandPalette) Render(width, height int) []string {
	if !cp.active {
		return nil
	}

	boxWidth, start, end := cp.Layout(width, height)

	var lines []string
	// Header: ╭─ Actions ─────────────────────────────────────────╮
	title := " Actions "
	remainingWidth := boxWidth - VisibleLen(title) - 3
	if remainingWidth < 0 {
		remainingWidth = 0
	}
	header := fmt.Sprintf("%s%s%s%s%s",
		cp.theme.BoxTopLeft,
		cp.theme.BoxHoriz,
		cp.theme.Colorize(cp.theme.Marshal, title),
		strings.Repeat(cp.theme.BoxHoriz, remainingWidth),
		cp.theme.BoxTopRight,
	)
	lines = append(lines, header)

	// Search input line: │ > query_                                             │
	searchPrompt := fmt.Sprintf("> %s_", string(cp.query))
	searchLine := fmt.Sprintf("%s %s%s",
		cp.theme.BoxVert,
		PadCell(searchPrompt, boxWidth-3),
		cp.theme.BoxVert,
	)
	lines = append(lines, searchLine)

	// Separator: ├───────────────────────────────────────────────────────┤
	sep := fmt.Sprintf("%s%s%s",
		cp.theme.BoxTRight,
		strings.Repeat(cp.theme.BoxHoriz, boxWidth-2),
		cp.theme.BoxTLeft,
	)
	lines = append(lines, sep)

	// Action items: Layout gives the half-open [start, end) range the box
	// shows, the same range the mouse hit code records regions for.
	if len(cp.filtered) == 0 {
		emptyMsg := "  (No matching actions)"
		lines = append(lines, fmt.Sprintf("%s %s%s",
			cp.theme.BoxVert,
			PadCell(emptyMsg, boxWidth-3),
			cp.theme.BoxVert,
		))
	} else {
		for i := start; i < end; i++ {
			act := cp.filtered[i]
			isSel := i == cp.selected

			catBadge := fmt.Sprintf("[%s]", act.Category)
			title := act.Title
			// The right column shows the row's key; a row with none shows
			// nothing. It never shows the row's /command text: the palette
			// lists actions, and typing still filters on the command
			// (paletteMatch), so it stays findable.
			cmd := act.Shortcut

			// Every row is the same visible width: a two-column marker followed
			// by rowText. Padding rowText to a width that already excludes the
			// marker keeps the selected and unselected rows identical, so the
			// right edge stays straight.
			contentWidth := boxWidth - 4
			rowWidth := contentWidth - 2

			leftPart := PadCell(catBadge, 10) + " " + title
			cmdWidth := VisibleLen(cmd)
			if VisibleLen(leftPart)+cmdWidth+2 > rowWidth {
				leftPart = PadCell(leftPart, rowWidth-cmdWidth-2)
			}
			rowText := PadCell(leftPart, rowWidth-cmdWidth-2) + "  " + cmd
			rowText = PadCell(rowText, rowWidth)

			var lineContent string
			if isSel {
				lineContent = cp.theme.Colorize(cp.theme.Reverse, "▶ "+rowText)
			} else if act.Disabled {
				lineContent = "  " + cp.theme.Colorize(cp.theme.Muted, rowText)
			} else {
				lineContent = "  " + rowText
			}

			lines = append(lines, fmt.Sprintf("%s %s%s",
				cp.theme.BoxVert,
				PadCell(lineContent, boxWidth-3),
				cp.theme.BoxVert,
			))
		}
	}

	// Footer: the keys when the selected row can run, or that row's reason
	// when it cannot.
	footerText := " Enter run · ↑↓ choose · type to filter · Esc close "
	if cp.selected >= 0 && cp.selected < len(cp.filtered) && cp.filtered[cp.selected].Disabled {
		footerText = " " + cp.filtered[cp.selected].Reason + " "
	}
	footerPad := boxWidth - VisibleLen(footerText) - 3
	if footerPad < 0 {
		footerPad = 0
	}
	footer := fmt.Sprintf("%s%s%s%s%s",
		cp.theme.BoxBottomLeft,
		cp.theme.BoxHoriz,
		cp.theme.Colorize(cp.theme.Muted, footerText),
		strings.Repeat(cp.theme.BoxHoriz, footerPad),
		cp.theme.BoxBottomRight,
	)
	lines = append(lines, footer)

	return lines
}
