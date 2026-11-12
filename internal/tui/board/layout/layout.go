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

// PaneID names a Board pane.
type PaneID string

const (
	Inbox     PaneID = "inbox"
	Tasks     PaneID = "tasks"
	Lanes     PaneID = "lanes"
	Activity  PaneID = "activity"
	Inspector PaneID = "inspector"
	Vitals    PaneID = "vitals" // row 0: provider limits and host vitals, on every view
	Header    PaneID = "header"
	Footer    PaneID = "footer"
	Notice    PaneID = "notice" // below the minimum size: says the terminal is too small
)

// priority is what survives when rows run out, most important first. Vitals,
// header and footer are one row each and never collapse.
var priority = map[PaneID]int{Inbox: 0, Tasks: 1, Lanes: 2, Activity: 3, Inspector: 4}

// Density is how much a pane shows per item. Width picks full or compact;
// height decides whether a pane keeps its rows, falls to its one-row strip,
// or goes.
type Density string

const (
	Full    Density = "full"
	Compact Density = "compact"
	Strip   Density = "strip" // one row
	// Brief is the tasks pane of a narrow Home (80x24 to ~100 columns,
	// mockups 05 and 06): a summary heading and a few rows of id, glyph
	// and short scope.
	Brief Density = "brief"
)

// BriefTasks is the rows a Brief tasks pane takes: its heading, five tasks
// and a "… N more" line.
const BriefTasks = 7

// Pane is one placed pane.
type Pane struct {
	ID      PaneID
	Rect    Rect
	Density Density
}

// Minimum widths of the panes that carry columns of their own.
const (
	MinTasks     = 56
	MinLanes     = 44
	MinInspector = 36
)

// Breakpoints, in the Board's own columns.
const (
	MinCols      = 80  // Home below this shows the inbox and a notice
	MinRows      = 24  // either mode below this shows the inbox and a notice
	TwoColumns   = 110 // tasks and inbox left, lanes and activity right
	FullDensity  = 160 // every pane at full density
	DockedColumn = 200 // the inspector docks as a third column
	LongBars     = 140 // header and footer in their long form
	FullOverlay  = 100 // below this an overlay takes the whole body
)

// minPaneRows is the least a pane gets unless it is a strip: a heading, one
// item and a "… N more" line.
const minPaneRows = 3

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

// slot is one pane as a column wants it, before rows are dealt out.
type slot struct {
	id      PaneID
	density Density // at the column's width
	want    int     // rows it asks for before the elastic pane takes the rest
	strip   bool    // may fall to a one-row strip
	elastic bool    // takes the rows nobody else wanted
	// most is the rows the pane's content fills, 0 for no limit. An
	// elastic pane grows no further, so its neighbour follows its last
	// row and what is left stays blank at the bottom, as in the prototype.
	most int
}

// column is a width and its panes, top to bottom.
type column struct {
	w     int
	slots []slot
}

// Counts is how many items each pane has to show. Split sizes its panes by
// them; Home lays out by size alone and ignores them.
type Counts struct{ Lanes, Tasks, Inbox, Activity int }

// Busy is what Compute assumes when the caller does not know the counts: a
// run with every pane holding more than it can show.
var Busy = Counts{Lanes: 12, Tasks: 53, Inbox: 2, Activity: 4}

// ChipLanes is the Split width below which lanes are always one row of
// chips: a lane row no longer fits its worker, task, time and action.
const ChipLanes = 70

// Compute lays the Board out for a terminal of cols by rows, its panes as
// full as Busy.
func Compute(cols, rows int, mode Mode) Layout {
	return ComputeFor(cols, rows, mode, Busy)
}

// ComputeFor lays the Board out for a terminal of cols by rows holding
// counts items.
func ComputeFor(cols, rows int, mode Mode, counts Counts) Layout {
	l := Layout{Cols: cols, Rows: rows, Mode: mode}
	if cols <= 0 || rows <= 0 {
		return l
	}
	minCols := MinCols
	if mode == Split {
		minCols = MinTasks
	}
	if cols < minCols || rows < MinRows {
		// Too small to lay out the Board: what can still be decided is
		// shown, and the notice says why the rest is not. The vitals row
		// stays on top even here.
		if rows == 1 {
			l.Panes = []Pane{{Notice, Rect{0, 0, cols, 1}, Strip}}
			l.Body = Rect{0, 1, cols, rows - 1}
			return l
		}
		l.Panes = []Pane{{Vitals, Rect{0, 0, cols, 1}, Strip}, {Notice, Rect{0, 1, cols, 1}, Strip}}
		l.Body = Rect{0, 2, cols, rows - 2}
		if rows > 2 {
			l.Panes = append(l.Panes, Pane{Inbox, l.Body, Compact})
		}
		return l
	}

	bars := Compact
	if cols >= LongBars {
		bars = Full
	}
	l.Body = Rect{0, 2, cols, rows - 3}
	l.Panes = []Pane{{Vitals, Rect{0, 0, cols, 1}, bars}, {Header, Rect{0, 1, cols, 1}, bars}, {Footer, Rect{0, rows - 1, cols, 1}, bars}}
	if mode == Split && cols < TwoColumns {
		l.Panes = append(l.Panes, split(l.Body, counts)...)
		return l
	}
	// Home, and a Board beside the chat that is at least TwoColumns wide:
	// tasks and inbox left, lanes and activity right, as the prototype
	// lays out its board. Only Home docks the inspector.
	x := 0
	for i, c := range columns(cols, counts, mode == Home) {
		if i > 0 {
			l.Rules = append(l.Rules, Rect{x, l.Body.Y, 1, l.Body.H})
			x++
		}
		l.Panes = append(l.Panes, stack(Rect{x, l.Body.Y, c.w, l.Body.H}, c.slots)...)
		x += c.w
	}
	return l
}

// columns splits the width, less one cell for each rule between columns.
// The tasks column takes 5/9 of what the inspector leaves, the proportion of
// mockups 01 and 07 and of the prototype's 1.25fr : 1fr board; at the
// narrowest two-column width that leaves the lanes 49 columns, above
// MinLanes. The tasks and lanes panes grow only to the rows their items fill.
func columns(cols int, c Counts, dock bool) []column {
	// An empty pane is its heading and column heads: it does not take the
	// column's spare rows, so the activity follows "LANES 0 of 12" and
	// the inbox the last task, as when they hold rows.
	tasksMost, lanesMost := 2+max(c.Tasks, 0), 2+max(c.Lanes, 0)
	if cols < TwoColumns {
		// The doc's 80x24 and ~100 columns rows (mockups 05, 06): the
		// inbox in full, five brief task rows, the lanes as one strip, and
		// one activity line at 80 columns, two at 100.
		activity := 2
		if cols >= FullOverlay {
			activity = 3
		}
		return []column{{cols, []slot{
			// The inbox in full: a heading and two rows an item, up to
			// three items, so a run's stop never hides a hard gate.
			{Inbox, Full, max(5, 1+2*min(c.Inbox, 3)), false, false, 0},
			{Tasks, Brief, BriefTasks, true, true, min(BriefTasks, max(tasksMost, minPaneRows))},
			{Lanes, Strip, 1, true, false, 0},
			{Activity, Compact, activity, true, false, 0},
		}}}
	}
	d, inbox, activity := Compact, 5, 3
	if cols >= FullDensity {
		d, inbox, activity = Full, 7, 5
	}
	// The inbox asks for a heading and two rows an item, up to three
	// items, as the single column does, so no entry the header counts is
	// cut off below its fixed share.
	inbox = max(inbox, 1+2*min(c.Inbox, 3))
	left := []slot{{Tasks, d, 12, true, true, tasksMost}, {Inbox, d, inbox, false, false, 0}}
	right := []slot{{Lanes, d, 12, true, true, lanesMost}, {Activity, d, activity, true, false, 0}}
	if cols < DockedColumn || !dock {
		lw := (cols - 1) * 5 / 9
		return []column{{lw, left}, {cols - 1 - lw, right}}
	}
	// The docked inspector takes about 2/9 of the width, as in mockup 07.
	iw := max(MinInspector, cols*2/9)
	rest := cols - 2 - iw
	lw := rest * 5 / 9
	return []column{{lw, left}, {rest - lw, right}, {iw, []slot{{Inspector, Full, 0, false, true, 0}}}}
}

// split stacks a Board narrower than TwoColumns beside the chat in the
// prototype's order, tasks, inbox, lanes, activity (owner decision
// 2026-11-06), one blank row between neighbours, sized by what they hold;
// rows nobody fills stay blank at the bottom. An empty
// lanes, activity or inbox pane is its one-row strip ("LANES 0 of 12",
// "INBOX empty"), and below ChipLanes, or with no room for two lane rows,
// the lanes are a row of chips. The
// others ask for their content, inbox first, and tasks takes every row
// left over.
func split(body Rect, c Counts) []Pane {
	type part struct {
		id        PaneID
		h, want   int
		d         Density
		collapses bool // may fall back to its strip when rows run out
	}
	sized := func(id PaneID, n, want int, chips bool) part {
		if n <= 0 || chips {
			return part{id, 1, 1, Strip, false}
		}
		return part{id, minPaneRows, max(minPaneRows, want), Compact, true}
	}
	parts := []part{
		// Tasks always shows: a heading, the column heads and its rows.
		{Tasks, minPaneRows, max(minPaneRows, min(2+c.Tasks, 10)), Compact, false},
		// A heading and two rows an item: the request and its action.
		sized(Inbox, c.Inbox, 1+2*min(c.Inbox, 3), false),
		// A heading, the column heads and one row per lane, as many
		// as the old fixed split ever gave them.
		sized(Lanes, c.Lanes, min(2+c.Lanes, 10), body.W < ChipLanes),
		sized(Activity, c.Activity, 1+min(c.Activity, 5), false),
	}
	free := body.H - (len(parts) - 1)
	for _, p := range parts {
		free -= p.h
	}
	// Below the minimum rows the lowest priority panes fall to strips.
	for _, id := range []PaneID{Activity, Lanes, Inbox} {
		for i := range parts {
			if free < 0 && parts[i].id == id && parts[i].collapses {
				free += parts[i].h - 1
				parts[i].h, parts[i].want, parts[i].d = 1, 1, Strip
			}
		}
	}
	for _, id := range []PaneID{Inbox, Tasks, Lanes, Activity} {
		for i := range parts {
			if parts[i].id == id {
				grow := max(0, min(parts[i].want-parts[i].h, free))
				parts[i].h += grow
				free -= grow
			}
		}
	}
	// Lanes that would show their heading and column heads but fewer
	// than two lanes read better as chips; their rows go to tasks.
	if l := &parts[2]; l.d != Strip && l.h-2 < min(c.Lanes, 2) {
		free += l.h - 1
		l.h, l.d = 1, Strip
	}
	out := make([]Pane, 0, len(parts))
	y := body.Y
	for _, p := range parts {
		if p.id == Tasks {
			// Tasks grows to the rows its items fill; what is left stays
			// blank below the last pane instead of opening a gap above
			// the inbox.
			p.h += max(0, min(free, 2+c.Tasks-p.h))
		}
		out = append(out, Pane{p.id, Rect{body.X, y, body.W, p.h}, p.d})
		y += p.h + 1
	}
	return out
}

// stack deals a column's rows out by priority, then places the panes top to
// bottom with one blank row between neighbours. Every pane first gets its
// smallest form, then its wanted rows, then the elastic pane takes what is
// left; a pane whose smallest form does not fit is not shown.
func stack(r Rect, slots []slot) []Pane {
	rows := make([]int, len(slots))
	dens := make([]Density, len(slots))
	free := func() int {
		used, shown := 0, 0
		for _, n := range rows {
			if n > 0 {
				used += n
				shown++
			}
		}
		return r.H - used - max(shown-1, 0)
	}
	order := make([]int, len(slots))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return priority[slots[a].id] - priority[slots[b].id] })

	for _, i := range order {
		n, d := minPaneRows, slots[i].density
		if slots[i].strip {
			n, d = 1, Strip
		}
		gap := 0
		if free() < r.H {
			gap = 1
		}
		if n+gap <= free() {
			rows[i], dens[i] = n, d
		}
	}
	for _, i := range order {
		s := slots[i]
		if rows[i] == 0 || s.density == Strip {
			continue
		}
		want := s.want
		if s.most > 0 {
			want = min(want, max(s.most, minPaneRows))
		}
		// A pane that wants fewer than minPaneRows (an activity heading
		// and its one line at 80x24) gets what it wants.
		if n := min(want, rows[i]+free()); n >= min(minPaneRows, s.want) && n > rows[i] {
			rows[i], dens[i] = n, s.density
		}
	}
	for i, s := range slots {
		if s.elastic && rows[i] > 0 && dens[i] == s.density {
			grow := free()
			if s.most > 0 {
				grow = min(grow, max(0, s.most-rows[i]))
			}
			rows[i] += grow
			break
		}
	}

	var out []Pane
	y := r.Y
	for i, s := range slots {
		if rows[i] == 0 {
			continue
		}
		out = append(out, Pane{s.id, Rect{r.X, y, r.W, rows[i]}, dens[i]})
		y += rows[i] + 1
	}
	return out
}

// Overlay is where an overlay at depth goes; depth 0 is the first one opened.
// It is centred in the body, each deeper overlay inset one step further,
// and never shrinks below the inspector's width. Below FullOverlay columns
// every overlay takes the whole body. The Board underneath keeps its layout.
func (l Layout) Overlay(depth int) Rect {
	if l.Cols < FullOverlay {
		return l.Body
	}
	r := l.Body
	for d := 0; d <= depth; d++ {
		dx, dy := l.Body.W/20+2*d, 1+d
		next := Rect{l.Body.X + dx, l.Body.Y + dy, l.Body.W - 2*dx, l.Body.H - 2*dy}
		if next.W < MinInspector || next.H < minPaneRows {
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
// narrower than its tasks pane but the chat can spare the difference and
// stay at least MinChat wide, the Board gets exactly its tasks pane width.
// ok is false when the Board still cannot reach its tasks pane: the Marshal
// then takes the whole window instead.
func SplitWidths(windowCols int) (marshal, board int, ok bool) {
	marshal = (windowCols - 1) / 2
	if windowCols-1-marshal < MinTasks && windowCols-1-MinTasks >= MinChat {
		marshal = windowCols - 1 - MinTasks
	}
	board = windowCols - 1 - marshal
	return marshal, board, board >= MinTasks
}

// Viewport is one pane's scroll and selection. The caller keeps one per pane
// across frames; Compute never sees it, so re-running Compute on a resize
// leaves both alone, and Fit reconciles them with the new height.
type Viewport struct {
	Top int // first item shown
	Sel int // selected item
}

// Fit keeps Sel (clamped to the n items) and moves Top as little as possible
// so Sel stays among the rows visible items and no rows are wasted past the
// end.
func (v Viewport) Fit(rows, n int) Viewport {
	if n <= 0 {
		return Viewport{}
	}
	v.Sel = min(max(v.Sel, 0), n-1)
	rows = max(rows, 1)
	v.Top = min(v.Top, n-rows)
	v.Top = max(v.Top, v.Sel-rows+1, 0)
	v.Top = min(v.Top, v.Sel)
	return v
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
