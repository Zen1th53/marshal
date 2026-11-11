package tui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/inspector"
)

// boardBar reports whether a pane is one of the Board's full-width bars: the
// vitals row, the header and the footer. The body views keep these and paint
// their own content between them.
func boardBar(id layout.PaneID) bool {
	return id == layout.Vitals || id == layout.Header || id == layout.Footer
}

// paintBoard places each pane's lines in its rect and returns the frame, one
// line per terminal row, each exactly l.Cols cells wide. A pane gets its rect
// and nothing else: lines past its height are dropped and every line is cut
// or padded to its width, so no widget can push a neighbour.
func paintBoard(l layout.Layout, th *Theme, content func(layout.Pane) []string) []string {
	type span struct {
		x, w int
		s    string
	}
	rows := make([][]span, l.Rows)
	for _, p := range l.Panes {
		lines := content(p)
		for i := range p.Rect.H {
			s := ""
			if i < len(lines) {
				s = lines[i]
			}
			rows[p.Rect.Y+i] = append(rows[p.Rect.Y+i], span{p.Rect.X, p.Rect.W, PadCell(s, p.Rect.W)})
		}
	}
	rule := th.Colorize(th.Border, th.BoxVert)
	for _, r := range l.Rules {
		for y := r.Y; y < r.Y+r.H; y++ {
			rows[y] = append(rows[y], span{r.X, 1, rule})
		}
	}
	out := make([]string, l.Rows)
	for y, spans := range rows {
		slices.SortFunc(spans, func(a, b span) int { return cmp.Compare(a.x, b.x) })
		var b strings.Builder
		x := 0
		for _, s := range spans {
			b.WriteString(strings.Repeat(" ", s.x-x))
			b.WriteString(s.s)
			x = s.x + s.w
		}
		b.WriteString(strings.Repeat(" ", l.Cols-x))
		out[y] = b.String()
	}
	return out
}

// paintBoardInspector uses the same inspector in the docked third column or
// over the Board body, according to the layout's inspector pane. The caller
// owns v, so its navigation (all events, the walk along the delegation
// chain) survives repaints.
// hit, when not nil, records each button of the inspector's action row.
func paintBoardInspector(l layout.Layout, th *Theme, v *inspector.View, content func(layout.Pane) []string, hit func(boardHit)) []string {
	frame := paintBoard(l, th, func(p layout.Pane) []string {
		if p.ID == layout.Inspector {
			lines := dockedInspector(th, v, p.Rect.W, p.Rect.H)
			inspectorHits(v, lines[min(1, len(lines)):], p.Rect.Y+1, p.Rect.X, p.Rect.W, p.Rect.Y+p.Rect.H, hit)
			return lines
		}
		return content(p)
	})
	if _, docked := l.Pane(layout.Inspector); docked {
		return frame
	}
	// The prototype uses a solid, centred dialog. Give it an opaque panel
	// background so no Board text can show through empty inspector cells.
	w := min(86, l.Body.W-4)
	if w < 30 {
		w = l.Body.W
	}
	lines := v.Render(w-4, l.Body.H-4)
	h := min(l.Body.H, len(lines)+4)
	r := layout.Rect{X: (l.Cols - w) / 2, Y: l.Body.Y + (l.Body.H-h)/2, W: w, H: h}
	inspectorHits(v, lines, r.Y+2, r.X+2, r.W-4, r.Y+r.H-1, hit)
	out := slices.Clone(frame)
	for y := l.Body.Y; y < l.Body.Y+l.Body.H; y++ {
		plain := StripANSI(out[y])
		out[y] = th.Colorize(th.Dim, plain)
		if y < r.Y || y >= r.Y+r.H {
			continue
		}
		left := th.Colorize(th.Dim, cellRange(plain, 0, r.X))
		right := th.Colorize(th.Dim, cellRange(plain, r.X+r.W, l.Cols))
		var middle string
		if y == r.Y || y == r.Y+r.H-1 {
			middle = "╭" + strings.Repeat("─", r.W-2) + "╮"
			if y == r.Y+r.H-1 {
				middle = "╰" + strings.Repeat("─", r.W-2) + "╯"
			}
		}
		if middle == "" {
			inner := ""
			if i := y - r.Y - 2; i >= 0 && i < len(lines) {
				inner = lines[i]
				if isInspectorSection(inner) {
					inner = boardVisual{theme: th}.fg("accent", inner)
				} else {
					inner = styleActionRow(th, v, r.W-4, inner)
				}
			}
			middle = "│ " + PadCell(inner, r.W-4) + " │"
		}
		visual := boardVisual{theme: th}
		if visual.code("panel", true) == "" {
			out[y] = left + middle + right
		} else {
			out[y] = left + visual.bg("panel", middle) + right
		}
	}
	return out
}

// dockedInspector is the inspector in its own column at ultrawide sizes,
// titled like the Board's panes ("INSPECTOR T-023 · docked", mockup 07) with
// its section headings in the pane title weight.
func dockedInspector(th *Theme, v *inspector.View, w, h int) []string {
	bv := boardVisual{theme: th}
	if h <= 0 {
		return nil
	}
	out := []string{bv.title("INSPECTOR", strings.TrimSpace(v.TaskID()+" · docked"), false, w)}
	for _, line := range v.Render(w, h-1) {
		if isInspectorSection(line) {
			line = bv.bold(bv.fg("white", line))
		} else {
			line = styleActionRow(th, v, w, line)
		}
		out = append(out, line)
	}
	return out
}

// styleActionRow draws a line of the inspector's action rows (laid out
// width cells wide) with each key as a button, as the inbox draws its
// buttons; any other line comes back as it is.
func styleActionRow(th *Theme, v *inspector.View, width int, line string) string {
	rows, buttons := inspector.ActionRows(v.Actions(), width)
	i := slices.Index(rows, line)
	if i < 0 {
		return line
	}
	bv := boardVisual{theme: th}
	var b strings.Builder
	x := 0
	for _, btn := range buttons {
		if btn.Row != i {
			continue
		}
		b.WriteString(strings.Repeat(" ", btn.X0-x))
		b.WriteString(bv.bg("rule", "["+string(btn.Key)+"]") + " " + btn.Label)
		x = btn.X1
	}
	return b.String()
}

// inspectorHits records the action rows' buttons: lines are the
// inspector's lines, laid out w cells wide and painted from frame row y
// and cell x on, above row end.
func inspectorHits(v *inspector.View, lines []string, y, x, w, end int, hit func(boardHit)) {
	if hit == nil {
		return
	}
	rows, buttons := inspector.ActionRows(v.Actions(), w)
	if len(rows) == 0 {
		return
	}
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = StripANSI(l)
	}
	for i := range plain {
		if !slices.Equal(plain[i:min(len(plain), i+len(rows))], rows) {
			continue
		}
		for _, btn := range buttons {
			if row := y + i + btn.Row; row < end && btn.X0 < w {
				hit(boardHit{row: row, x0: x + btn.X0, x1: x + min(w, btn.X1), kind: hitInspect, id: v.TaskID(), action: btn.Key})
			}
		}
		return
	}
}

// isInspectorSection reports whether an inspector line is a section heading.
func isInspectorSection(line string) bool {
	switch line {
	case "IDENTITY", "AUTHORITY", "EVIDENCE", "TIMELINE", "ACTIONS":
		return true
	}
	return false
}

// overlayBoard draws the overlay at depth over a painted Board. The body
// underneath dims to plain text and keeps its layout; header and footer stay
// as they are. Drawing the next overlay of a stack over the result dims the
// one below it, so only the top of the stack is bright.
func overlayBoard(frame []string, l layout.Layout, th *Theme, depth int, lines []string) []string {
	r := l.Overlay(depth)
	out := slices.Clone(frame)
	for y := l.Body.Y; y < l.Body.Y+l.Body.H; y++ {
		plain := StripANSI(out[y])
		if y < r.Y || y >= r.Y+r.H {
			out[y] = th.Colorize(th.Dim, plain)
			continue
		}
		s := ""
		if i := y - r.Y; i < len(lines) {
			s = lines[i]
		}
		out[y] = th.Colorize(th.Dim, cellRange(plain, 0, r.X)) + PadCell(s, r.W) + th.Colorize(th.Dim, cellRange(plain, r.X+r.W, l.Cols))
	}
	return out
}

// cellRange returns the cells [from, to) of a line without escapes, padded to
// to-from cells. A wide rune cut by either edge leaves spaces for its cells
// inside the range: half a glyph cannot be drawn.
func cellRange(plain string, from, to int) string {
	var b strings.Builder
	x := 0
	for _, r := range plain {
		w := RuneWidth(r)
		switch {
		case x >= to:
		case x >= from && x+w <= to:
			b.WriteRune(r)
		default:
			b.WriteString(strings.Repeat(" ", max(0, min(x+w, to)-max(x, from))))
		}
		x += w
	}
	b.WriteString(strings.Repeat(" ", max(0, to-max(x, from))))
	return b.String()
}
