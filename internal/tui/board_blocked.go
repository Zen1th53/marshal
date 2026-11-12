package tui

import (
	"strings"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// blockedPrefix is the plain-English lead of every Blocked line; it is painted
// in the failure colour, like other failure text.
const blockedPrefix = "I can't fix this safely:"

// paintBlocked paints the Blocked screen in the Board's body: one line per
// problem the Marshal cannot fix safely, each with the exact command to run,
// then the retry and quit keys. The header and footer stay; the chat does not
// start while this is shown.
func (w *Workspace) paintBlocked(l layout.Layout, th *Theme, v boardVisual) []string {
	out := make([]string, l.Rows)
	place := func(id layout.PaneID) {
		if p, ok := l.Pane(id); ok {
			lines := v.render(p)
			for i := 0; i < p.Rect.H && i < len(lines); i++ {
				out[p.Rect.Y+i] = PadCell(lines[i], l.Cols)
			}
		}
	}
	place(layout.Header)
	place(layout.Footer)
	body := l.Body
	if body.H <= 0 {
		return out
	}
	rows := v.blockedBody(w.launchBlockers(), body.W, body.H, body.Y)
	for i := 0; i < body.H; i++ {
		s := ""
		if i < len(rows) {
			s = rows[i]
		}
		out[body.Y+i] = PadCell(s, l.Cols)
	}
	if !v.unicode() {
		copy(out[body.Y:body.Y+body.H], v.ascii(out[body.Y:body.Y+body.H], l.Cols))
	}
	return out
}

// blockedBody is the Blocked screen's body for a w-by-h area whose first row is
// frame row y: the blocking lines, a gap, then the retry and quit keys.
func (v boardVisual) blockedBody(blockers []string, w, h, y int) []string {
	var out []string
	add := func(s string) {
		if len(out) < h {
			out = append(out, PadCell(s, w))
		}
	}
	hit := func(b boardHit) {
		if len(out) < h {
			b.row = y + len(out)
			v.hit(b)
		}
	}
	pad := "  "
	if w < 60 {
		pad = " "
	}
	textWidth := max(1, w-len(pad))
	for _, line := range blockers {
		for i, row := range wrap(line, textWidth) {
			if i == 0 && strings.HasPrefix(row, blockedPrefix) {
				row = v.fg("failed", blockedPrefix) + strings.TrimPrefix(row, blockedPrefix)
			}
			add(pad + row)
		}
	}
	if len(out) > 0 && len(out) < h {
		add("")
	}
	// The retry and quit keys. [r] is clickable through the same path its key
	// press takes.
	retry := v.bg("rule", "[r]") + " Retry"
	quit := v.bg("rule", "[q]") + " Quit"
	hit(boardHit{x0: len(pad), x1: len(pad) + VisibleLen("[r]"), kind: hitAction, id: "launch.retry"})
	add(pad + retry + "   " + quit)
	return out
}
