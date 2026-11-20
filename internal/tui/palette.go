package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
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
	// Core marks one of the owner's primary actions shown first when the
	// palette opens with an empty query. CoreOrder (1..N) fixes their
	// display order; CoreKey and CoreLabel are the single key and short
	// label shown in the core grid (falling back to Shortcut and Title).
	Core      bool
	CoreOrder int
	CoreKey   string
	CoreLabel string
}

func (a PaletteAction) coreKey() string {
	if a.CoreKey != "" {
		return a.CoreKey
	}
	return a.Shortcut
}

func (a PaletteAction) coreLabel() string {
	if a.CoreLabel != "" {
		return a.CoreLabel
	}
	return a.Title
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

// PaletteCell is the box-relative position of one clickable entry in
// cp.filtered: Row from the box top (0 is the top border) and columns
// [X0, X1) from the box left edge.
type PaletteCell struct {
	Index       int
	Row, X0, X1 int
}

func (cp *CommandPalette) coreCount() int {
	n := 0
	for _, a := range cp.actions {
		if a.Core {
			n++
		}
	}
	return n
}

func (cp *CommandPalette) coreMode() bool {
	return strings.TrimSpace(string(cp.query)) == "" && cp.coreCount() > 0
}

func (cp *CommandPalette) totalCount() int {
	return len(cp.actions) + len(cp.ctx.Selection) + len(cp.ctx.Others)
}

func (cp *CommandPalette) boxWidth(width int) int {
	bw := width - 8
	if bw > 76 {
		bw = 76
	}
	if bw < 40 {
		bw = width
	}
	return bw
}

func (cp *CommandPalette) coreColumns(width, boxWidth int) int {
	if width > 80 && boxWidth >= 74 {
		return 2
	}
	return 1
}

// Layout is the palette box's geometry for a width-by-height overlay: the
// box's width in cells and the half-open range [start, end) of cp.filtered
// the box shows. Render and the mouse hit code both read it, so a row's
// screen position is computed one way.
func (cp *CommandPalette) Layout(width, height int) (boxWidth, start, end int) {
	boxWidth = cp.boxWidth(width)
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

// Cells returns the box-relative clickable regions Render draws for
// cp.filtered, covering both the two-column core grid and the search list.
func (cp *CommandPalette) Cells(width, height int) []PaletteCell {
	boxWidth, start, end := cp.Layout(width, height)
	if cp.coreMode() && cp.coreColumns(width, boxWidth) == 2 {
		n := len(cp.filtered)
		if n == 0 {
			return nil
		}
		numRows := (n + 1) / 2
		maxRows := numRows
		if height > 0 && maxRows > height-5 {
			maxRows = max(1, height-5)
		}
		selRow := cp.selected % numRows
		rowStart := 0
		if selRow >= maxRows {
			rowStart = selRow - maxRows + 1
		}
		rowEnd := min(numRows, rowStart+maxRows)
		contentWidth := boxWidth - 4
		colW := (contentWidth - 2) / 2
		var cells []PaletteCell
		for r := rowStart; r < rowEnd; r++ {
			row := 3 + (r - rowStart)
			cells = append(cells, PaletteCell{Index: r, Row: row, X0: 2, X1: 2 + colW})
			if right := r + numRows; right < n {
				cells = append(cells, PaletteCell{Index: right, Row: row, X0: 2 + colW + 2, X1: 2 + colW + 2 + colW})
			}
		}
		return cells
	}
	cells := make([]PaletteCell, 0, end-start)
	for i := start; i < end; i++ {
		cells = append(cells, PaletteCell{Index: i, Row: 3 + (i - start), X0: 0, X1: boxWidth})
	}
	return cells
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

// filter orders the entries the query matches. When the query is empty and
// core actions are marked, cp.filtered holds only the core actions in their
// CoreOrder so the empty-query palette opens directly to them. Otherwise
// (typing a query, or when no actions are marked Core), all entries are
// searched and ranked by match quality, then context (selection, focused pane,
// other inbox actions), then core actions first on ties, then enabled before
// disabled. The sort is stable, so one state always gives one order.
func (cp *CommandPalette) filter() {
	raw := strings.TrimSpace(string(cp.query))
	query := strings.ToLower(raw)
	if query == "" && cp.coreCount() > 0 {
		cp.filtered = cp.filtered[:0]
		for _, a := range cp.actions {
			if a.Core {
				cp.filtered = append(cp.filtered, a)
			}
		}
		slices.SortStableFunc(cp.filtered, func(x, y PaletteAction) int {
			return x.CoreOrder - y.CoreOrder
		})
		if cp.selected >= len(cp.filtered) {
			if len(cp.filtered) > 0 {
				cp.selected = len(cp.filtered) - 1
			} else {
				cp.selected = 0
			}
		}
		return
	}
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
		if x.a.Core != y.a.Core {
			if x.a.Core {
				return -1
			}
			return 1
		}
		if x.a.Core && y.a.Core && x.a.CoreOrder != y.a.CoreOrder {
			return x.a.CoreOrder - y.a.CoreOrder
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
	if slices.Contains(strings.Fields(a.Shortcut), raw) || (a.CoreKey != "" && a.CoreKey == raw) {
		return 3
	}
	isSep := func(r rune) bool {
		return r == ' ' || r == ':' || r == '·' || r == '(' || r == ')' || r == ','
	}
	for _, s := range []string{a.Title, a.CoreLabel} {
		for _, f := range strings.FieldsFunc(strings.ToLower(s), isSep) {
			if strings.HasPrefix(f, query) {
				return 2
			}
		}
	}
	target := strings.ToLower(fmt.Sprintf("%s %s %s %s %s %s %s", a.Category, a.Title, a.CoreLabel, a.Description, a.Command, a.Shortcut, a.CoreKey))
	if strings.Contains(target, query) {
		return 1
	}
	return 0
}

var paletteASCIIReplacer = strings.NewReplacer(
	"…", ".",
	"·", ".",
	"─", "-",
	"│", "|",
	"╭", "+", "╮", "+", "╰", "+", "╯", "+", "├", "+", "┤", "+",
	"▶", ">", "►", ">", "▸", ">",
	"↑", "Up", "↓", "Dn",
	"←", "<-", "→", "->", "⇄", "<->",
	"⌫", "Bksp",
)

func (cp *CommandPalette) cleanText(s string) string {
	if cp.theme.Unicode {
		return s
	}
	s = paletteASCIIReplacer.Replace(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			if n := csiLen(s[i:]); n > 0 {
				b.WriteString(s[i : i+n])
				i += n
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r > 127 {
			b.WriteByte('?')
		} else {
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

func (cp *CommandPalette) padCell(s string, w int) string {
	if !cp.theme.Unicode {
		s = cp.cleanText(s)
	}
	out := PadCell(s, w)
	if !cp.theme.Unicode {
		out = strings.ReplaceAll(out, "…", ".")
	}
	return out
}

func (cp *CommandPalette) selMarker() string {
	if cp.theme.Unicode {
		return "▶ "
	}
	return "> "
}

func (cp *CommandPalette) formatCoreCell(act PaletteAction, isSel bool, colW int) string {
	const keyW = 6
	labelW := max(1, colW-2-keyW-2)
	key := cp.padCell(act.coreKey(), keyW)
	label := cp.padCell(act.coreLabel(), labelW)
	body := key + "  " + label
	if isSel {
		return cp.theme.Colorize(cp.theme.Reverse, cp.selMarker()+body)
	}
	if act.Disabled {
		return "  " + cp.theme.Colorize(cp.theme.Muted, body)
	}
	return "  " + cp.theme.Colorize(cp.theme.Marshal, key) + "  " + label
}

// Render renders the Command Palette box for display in the terminal.
// width and height constrain the maximum dimensions.
func (cp *CommandPalette) Render(width, height int) []string {
	if !cp.active {
		return nil
	}

	boxWidth, start, end := cp.Layout(width, height)

	var lines []string
	// Header: ╭─ ACTIONS  type to search all N ───────────────────╮
	var titlePlain, titleStyled string
	if total := cp.totalCount(); total > 0 {
		candidate := fmt.Sprintf(" ACTIONS  type to search all %d ", total)
		if VisibleLen(candidate)+3 <= boxWidth {
			titlePlain = candidate
			titleStyled = " " + cp.theme.Colorize(cp.theme.Marshal, "ACTIONS") +
				cp.theme.Colorize(cp.theme.Muted, fmt.Sprintf("  type to search all %d", total)) + " "
		}
	}
	if titlePlain == "" {
		titlePlain = " ACTIONS "
		titleStyled = " " + cp.theme.Colorize(cp.theme.Marshal, "ACTIONS") + " "
	}
	remainingWidth := boxWidth - VisibleLen(titlePlain) - 3
	if remainingWidth < 0 {
		remainingWidth = 0
	}
	header := fmt.Sprintf("%s%s%s%s%s",
		cp.theme.BoxTopLeft,
		cp.theme.BoxHoriz,
		titleStyled,
		strings.Repeat(cp.theme.BoxHoriz, remainingWidth),
		cp.theme.BoxTopRight,
	)
	lines = append(lines, header)

	// Search input line: │ > query_                                             │
	searchPrompt := fmt.Sprintf("> %s_", string(cp.query))
	searchLine := fmt.Sprintf("%s %s%s",
		cp.theme.BoxVert,
		cp.padCell(searchPrompt, boxWidth-3),
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

	// Action items: empty query shows the 8 core actions (two columns when
	// wider than 80 columns, one column at 80 columns) plus one faint "more"
	// line. Typing searches all commands in a single-column list.
	if cp.coreMode() {
		contentWidth := boxWidth - 4
		if cp.coreColumns(width, boxWidth) == 2 {
			n := len(cp.filtered)
			numRows := (n + 1) / 2
			maxRows := numRows
			if height > 0 && maxRows > height-5 {
				maxRows = max(1, height-5)
			}
			selRow := 0
			if numRows > 0 {
				selRow = cp.selected % numRows
			}
			rowStart := 0
			if selRow >= maxRows {
				rowStart = selRow - maxRows + 1
			}
			rowEnd := min(numRows, rowStart+maxRows)
			colW := (contentWidth - 2) / 2
			for r := rowStart; r < rowEnd; r++ {
				left := cp.formatCoreCell(cp.filtered[r], r == cp.selected, colW)
				right := strings.Repeat(" ", colW)
				if ri := r + numRows; ri < n {
					right = cp.formatCoreCell(cp.filtered[ri], ri == cp.selected, colW)
				}
				lineContent := left + "  " + right
				lines = append(lines, fmt.Sprintf("%s %s%s",
					cp.theme.BoxVert,
					cp.padCell(lineContent, boxWidth-3),
					cp.theme.BoxVert,
				))
			}
		} else {
			for i := start; i < end; i++ {
				lineContent := cp.formatCoreCell(cp.filtered[i], i == cp.selected, contentWidth)
				lines = append(lines, fmt.Sprintf("%s %s%s",
					cp.theme.BoxVert,
					cp.padCell(lineContent, boxWidth-3),
					cp.theme.BoxVert,
				))
			}
		}
		if more := cp.totalCount() - len(cp.filtered); more > 0 {
			dash := cp.theme.BoxHoriz + cp.theme.BoxHoriz
			dot := "·"
			if !cp.theme.Unicode {
				dot = "."
			}
			moreText := fmt.Sprintf("%s %d more %s type a word, e.g. \"retry\", \"budget\", \"telemetry\"", dash, more, dot)
			moreLine := "  " + cp.theme.Colorize(cp.theme.Muted, cp.padCell(moreText, max(1, boxWidth-5)))
			lines = append(lines, fmt.Sprintf("%s %s%s",
				cp.theme.BoxVert,
				cp.padCell(moreLine, boxWidth-3),
				cp.theme.BoxVert,
			))
		}
	} else if len(cp.filtered) == 0 {
		emptyMsg := "  (No matching actions)"
		lines = append(lines, fmt.Sprintf("%s %s%s",
			cp.theme.BoxVert,
			cp.padCell(emptyMsg, boxWidth-3),
			cp.theme.BoxVert,
		))
	} else {
		for i := start; i < end; i++ {
			act := cp.filtered[i]
			isSel := i == cp.selected

			catBadge := fmt.Sprintf("[%s]", act.Category)
			title := cp.cleanText(act.Title)
			// The right column shows the row's key; a row with none shows
			// nothing. It never shows the row's /command text: the palette
			// lists actions, and typing still filters on the command
			// (paletteMatch), so it stays findable.
			cmd := cp.cleanText(act.Shortcut)

			// Every row is the same visible width: a two-column marker followed
			// by rowText. Padding rowText to a width that already excludes the
			// marker keeps the selected and unselected rows identical, so the
			// right edge stays straight.
			contentWidth := boxWidth - 4
			rowWidth := contentWidth - 2

			leftPart := cp.padCell(catBadge, 10) + " " + title
			cmdWidth := VisibleLen(cmd)
			if VisibleLen(leftPart)+cmdWidth+2 > rowWidth {
				leftPart = cp.padCell(leftPart, rowWidth-cmdWidth-2)
			}
			rowText := cp.padCell(leftPart, rowWidth-cmdWidth-2) + "  " + cmd
			rowText = cp.padCell(rowText, rowWidth)

			var lineContent string
			if isSel {
				lineContent = cp.theme.Colorize(cp.theme.Reverse, cp.selMarker()+rowText)
			} else if act.Disabled {
				lineContent = "  " + cp.theme.Colorize(cp.theme.Muted, rowText)
			} else {
				lineContent = "  " + rowText
			}

			lines = append(lines, fmt.Sprintf("%s %s%s",
				cp.theme.BoxVert,
				cp.padCell(lineContent, boxWidth-3),
				cp.theme.BoxVert,
			))
		}
	}

	// Footer: the keys when the selected row can run, or that row's reason
	// when it cannot.
	var footerText string
	if cp.theme.Unicode {
		footerText = " Enter run · ↑↓ choose · type to filter · Esc close "
	} else {
		footerText = " Enter run . Up/Dn choose . type to filter . Esc close "
	}
	if cp.selected >= 0 && cp.selected < len(cp.filtered) && cp.filtered[cp.selected].Disabled && cp.filtered[cp.selected].Reason != "" {
		footerText = " " + cp.cleanText(cp.filtered[cp.selected].Reason) + " "
	}
	if maxFooter := boxWidth - 3; maxFooter > 0 && VisibleLen(footerText) > maxFooter {
		footerText = cp.padCell(footerText, maxFooter)
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
