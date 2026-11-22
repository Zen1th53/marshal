// Package layout decides where every Board pane goes. It is a pure function
// of the terminal size and the mode: widgets are handed a rect and a density
// and never compute a position, so one place owns the geometry and a resize
// is just another call.
package layout

import "slices"

// Rect is a block of terminal cells, zero-based, X across and Y down.
type Rect struct{ X, Y, W, H int }

// Mode is what else shares the Board's terminal. The caller derives it from
// tmux: the Board is in Split when its pane is narrower than its window,
// because the Marshal chat has been joined beside it.
type Mode string

const (
	Home  Mode = "home"  // the Board owns its terminal
	Split Mode = "split" // the Marshal chat is joined at the left; cols is the Board's half
)

// PaneID names a placed pane, or what holds the Board's keyboard focus
// (Inbox, Tasks, Lanes).
type PaneID string

const (
	Inbox  PaneID = "inbox"
	Tasks  PaneID = "tasks"
	Lanes  PaneID = "lanes"
	Vitals PaneID = "vitals" // row 0: provider limits and host vitals, on every view
	Header PaneID = "header"
	Footer PaneID = "footer"
	Notice PaneID = "notice" // below the minimum size: says the terminal is too small
)

// Density is how much a pane shows. The views paint their body at Full;
// the bars are Full from LongBars columns and Compact below; the too-small
// notice is a one-row Strip.
type Density string

const (
	Full    Density = "full"
	Compact Density = "compact"
	Strip   Density = "strip" // one row
)

// Pane is one placed pane.
type Pane struct {
	ID      PaneID
	Rect    Rect
	Density Density
}

// MinSplit is the narrowest Board pane SplitWidths gives beside the Marshal
// chat while the chat keeps MinChat; a view below it shows the too-small
// notice. The home screen itself draws at any width (board_home.go).
const MinSplit = 60

// Breakpoints, in the Board's own columns.
const (
	MinCols     = 80  // Home below this a view shows only the too-small notice
	MinRows     = 12  // either mode below this a view shows only the too-small notice
	TwoColumns  = 110 // the host view's second column
	LongBars    = 140 // header and footer in their long form
	FullOverlay = 100 // below this an overlay takes the whole body
)

// minPaneRows is the least rows an inset overlay keeps.
const minPaneRows = 3

// minOverlay is the narrowest an inset overlay gets.
const minOverlay = 36

// Layout is the geometry for one terminal size. Panes never overlap each
// other or a rule.
type Layout struct {
	Cols, Rows int
	Mode       Mode
	Panes      []Pane
	Rules      []Rect // one-cell-wide vertical rules between columns
	Body       Rect   // between header and footer; overlays sit inside it
}

// Pane returns the placed pane with that id, or false when this size does not
// show it.
func (l Layout) Pane(id PaneID) (Pane, bool) {
	i := slices.IndexFunc(l.Panes, func(p Pane) bool { return p.ID == id })
	if i < 0 {
		return Pane{}, false
	}
	return l.Panes[i], true
}

// Compute lays the Board out for a terminal of cols by rows: the vitals
// row, the header, the footer and the body between them, where a view
// paints (the home screen paints the whole frame itself). Below the minimum size there is only the vitals
// row and a one-row notice; the body is what is left under them.
func Compute(cols, rows int, mode Mode) Layout {
	l := Layout{Cols: cols, Rows: rows, Mode: mode}
	if cols <= 0 || rows <= 0 {
		return l
	}
	minCols := MinCols
	if mode == Split {
		minCols = MinSplit
	}
	if cols < minCols || rows < MinRows {
		if rows == 1 {
			l.Panes = []Pane{{Notice, Rect{0, 0, cols, 1}, Strip}}
			l.Body = Rect{0, 1, cols, rows - 1}
			return l
		}
		l.Panes = []Pane{{Vitals, Rect{0, 0, cols, 1}, Strip}, {Notice, Rect{0, 1, cols, 1}, Strip}}
		l.Body = Rect{0, 2, cols, rows - 2}
		return l
	}
	bars := Compact
	if cols >= LongBars {
		bars = Full
	}
	l.Body = Rect{0, 2, cols, rows - 3}
	l.Panes = []Pane{{Vitals, Rect{0, 0, cols, 1}, bars}, {Header, Rect{0, 1, cols, 1}, bars}, {Footer, Rect{0, rows - 1, cols, 1}, bars}}
	return l
}

// Overlay is where an overlay at depth goes; depth 0 is the first one opened.
// It is centred in the body, each deeper overlay inset one step further,
// and never shrinks below minOverlay columns. Below FullOverlay columns
// every overlay takes the whole body. The Board underneath keeps its layout.
func (l Layout) Overlay(depth int) Rect {
	if l.Cols < FullOverlay {
		return l.Body
	}
	r := l.Body
	for d := 0; d <= depth; d++ {
		dx, dy := l.Body.W/20+2*d, 1+d
		next := Rect{l.Body.X + dx, l.Body.Y + dy, l.Body.W - 2*dx, l.Body.H - 2*dy}
		if next.W < minOverlay || next.H < minPaneRows {
			break
		}
		r = next
	}
	return r
}

// MinChat is the narrowest Marshal chat the split gives up width for: below
// it the chat could no longer be read.
const MinChat = 40

// SplitWidths divides a window between the Marshal chat (left) and the Board
// (right), 50/50 around tmux's one-cell border. When the Board's half would be
// narrower than MinSplit but the chat can spare the difference and stay at
// least MinChat wide, the Board gets exactly MinSplit. ok is false when the
// Board still cannot reach MinSplit.
func SplitWidths(windowCols int) (marshal, board int, ok bool) {
	marshal = (windowCols - 1) / 2
	if windowCols-1-marshal < MinSplit && windowCols-1-MinSplit >= MinChat {
		marshal = windowCols - 1 - MinSplit
	}
	board = windowCols - 1 - marshal
	return marshal, board, board >= MinSplit
}

// WithoutVitals is l with its vitals row taken out: every pane, rule and the
// body move up one row and the layout is one row shorter. The caller lays out
// one row more than it has and calls this when the vitals row is painted
// elsewhere — Marshal's tmux status line, full width above every pane — so
// the Board keeps the geometry it has at that size and only loses the row.
// A layout with no vitals row comes back unchanged.
func (l Layout) WithoutVitals() Layout {
	i := slices.IndexFunc(l.Panes, func(p Pane) bool { return p.ID == Vitals })
	if i < 0 {
		return l
	}
	out := l
	out.Rows = max(0, l.Rows-1)
	out.Panes = nil
	for j, p := range l.Panes {
		if j == i {
			continue
		}
		p.Rect.Y--
		out.Panes = append(out.Panes, p)
	}
	out.Rules = nil
	for _, r := range l.Rules {
		r.Y--
		out.Rules = append(out.Rules, r)
	}
	out.Body.Y--
	return out
}
