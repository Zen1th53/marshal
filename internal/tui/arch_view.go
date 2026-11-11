package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// archPane is the Architecture view's one pane: the Board's body, between
// its header and footer.
const archPane layout.PaneID = "architecture"

// archSnapshot is the Architecture view's data: the last status read of the
// browser review and when it was read.
type archSnapshot struct {
	Status app.ArchitectureStatus
	// Loaded is set once a read has succeeded; At is when the last read
	// finished and Err why it failed, if it did.
	Loaded bool
	At     time.Time
	Err    string
}

// archPaint is what one frame of the Architecture view needs.
type archPaint struct {
	Snap archSnapshot
}

// archStatusLine is view 8: the browser review's state in one line. open
// reports where "[o] open" starts in the line's visible cells, -1 when
// absent. The line is plain English; the colours mark the one word the
// owner acts on (waiting / accepted) and the "[o]" button.
func (v boardVisual) archStatusLine(s archSnapshot) (line string, open int) {
	const head = "Architecture"
	const sep = " · "
	button := v.bg("rule", "[o]") + " open"
	if !s.Loaded {
		if s.Err != "" {
			return v.fg("dim", head+sep+"could not be read right now"), -1
		}
		return v.fg("dim", head+sep+"reading…"), -1
	}
	st := s.Status
	if !st.Exists {
		return v.fg("dim", head+sep+"not drafted yet · the Marshal drafts it after your goal"), -1
	}
	rev := fmt.Sprintf("rev %d", st.Revision)
	switch {
	case st.Accepted:
		plain := head + sep + rev + sep + "accepted" + sep
		coloured := v.fg("dim", head+sep+rev+sep) + v.fg("merged", "accepted") + v.fg("dim", sep)
		return coloured + button, VisibleLen(plain)
	case st.Answering:
		plain := head + sep + rev + sep + "the Marshal is answering your request" + sep
		return v.fg("dim", plain) + button, VisibleLen(plain)
	case st.Open > 0:
		reqs := fmt.Sprintf("%d open request%s", st.Open, plural(st.Open, "", "s"))
		plain := head + sep + rev + sep + "waiting for you" + sep + reqs + sep
		coloured := v.fg("dim", head+sep+rev+sep) + v.fg("waiting", "waiting for you") + v.fg("dim", sep+reqs+sep)
		return coloured + button, VisibleLen(plain)
	default:
		plain := head + sep + rev + sep + "waiting for you" + sep
		coloured := v.fg("dim", head+sep+rev+sep) + v.fg("waiting", "waiting for you") + v.fg("dim", sep)
		return coloured + button, VisibleLen(plain)
	}
}

// archRender paints the Architecture view into a w×h body: the one status
// line, then blank rows.
func (v boardVisual) archRender(m archPaint, w, h int) []string {
	line, _ := v.archStatusLine(m.Snap)
	return v.archWindow([]string{line}, w, h)
}

// archWindow pads or cuts the view's lines to a w×h body.
func (v boardVisual) archWindow(out []string, w, h int) []string {
	out = slices.Clone(out[:min(len(out), h)])
	for i := range out {
		out[i] = PadCell(out[i], w)
	}
	for len(out) < h {
		out = append(out, PadCell("", w))
	}
	if !v.unicode() {
		out = hostASCII(boardASCIIGlyphs(out), w)
	}
	return out
}

// wrap breaks text to width, so a reason is never truncated away. The contract
// requires the explanation to remain readable at any width.
func wrap(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var out []string
	line := ""
	for _, w := range words {
		candidate := w
		if line != "" {
			candidate = line + " " + w
		}
		if len([]rune(candidate)) > width && line != "" {
			out = append(out, line)
			line = w
			continue
		}
		line = candidate
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}
