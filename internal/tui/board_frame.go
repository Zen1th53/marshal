package tui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
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
