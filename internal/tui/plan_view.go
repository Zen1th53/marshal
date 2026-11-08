package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// planPane is the Plan view's one pane: the Board's body, between its
// header and footer.
const planPane layout.PaneID = "plan"

// planSnapshot is the Plan view's data: the last overview read and when
// it was read.
type planSnapshot struct {
	Overview store.PlanOverview
	// Loaded is set once a read has succeeded; At is when the last read
	// finished and Err why it failed, if it did.
	Loaded bool
	At     time.Time
	Err    string
}

// planPaint is what one frame of the Plan view needs.
type planPaint struct {
	Snap planSnapshot
	// Selected is the selected task's id.
	Selected string
}

// planGraph is the run's tasks laid out by dependency level: a task with
// no dependencies is on level 0, any other one level past its deepest
// dependency.
type planGraph struct {
	// Order is the tasks level by level, each level in plan order: the
	// order j and k move in.
	Order []store.PlanTask
	Level map[string]int
	// Levels is how many levels there are.
	Levels int
	// Down maps a task to the tasks that need it.
	Down map[string][]string
}

func newPlanGraph(tasks []store.PlanTask) planGraph {
	g := planGraph{Level: map[string]int{}, Down: map[string][]string{}}
	byID := map[string]store.PlanTask{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	visiting := map[string]bool{}
	var level func(id string) int
	level = func(id string) int {
		if l, ok := g.Level[id]; ok {
			return l
		}
		if visiting[id] {
			// A cycle the plan should not have: break it here.
			return 0
		}
		visiting[id] = true
		l := 0
		for _, d := range byID[id].DependsOn {
			if _, ok := byID[d]; ok {
				l = max(l, level(d)+1)
			}
		}
		visiting[id] = false
		g.Level[id] = l
		return l
	}
	for _, t := range tasks {
		g.Levels = max(g.Levels, level(t.ID)+1)
		for _, d := range t.DependsOn {
			if _, ok := byID[d]; ok {
				g.Down[d] = append(g.Down[d], t.ID)
			}
		}
	}
	for l := range g.Levels {
		for _, t := range tasks {
			if g.Level[t.ID] == l {
				g.Order = append(g.Order, t)
			}
		}
	}
	return g
}

// planTaskState is the operator's word for a task's state: the Board's
// when the Board has the task, which also knows a task in verification,
// else the run's.
func (v boardVisual) planTaskState(t store.PlanTask) model.TaskState {
	for _, r := range v.board.Tasks {
		if r.ID == t.ID {
			return r.State
		}
	}
	if st, ok := model.StateOf(t.State); ok {
		return st
	}
	return model.TaskState(t.State)
}

// planRender paints the Plan view into a w×h body.
func (v boardVisual) planRender(m planPaint, w, h int) []string {
	out := v.planLines(m, w, h)
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

// boardASCIIGlyphs replaces the Board's state glyphs that hostASCII does
// not know.
func boardASCIIGlyphs(lines []string) []string {
	r := strings.NewReplacer("✓", "v", "▸", ">", "↻", "~", "◆", "*", "⏎", "Enter")
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = r.Replace(l)
	}
	return out
}

// planLines lays the view out as the prototype does: the PLAN head, the
// dependency graph in columns by level, the selected task's needs and
// what it unblocks, and the plan's VERSIONS. The graph gets what the other
// sections leave of h and keeps the selected task in view.
func (v boardVisual) planLines(m planPaint, w, h int) []string {
	s := m.Snap
	o := s.Overview
	head := joinNonEmpty(" · ", o.RunID, planVersionHead(o, v.now))
	if s.Loaded {
		head = joinNonEmpty(" · ", head, fmt.Sprintf("%d tasks", len(o.Tasks)))
	}
	out := []string{v.title("PLAN", head, false, w)}
	if !s.Loaded {
		read := "reading the store…"
		if s.Err != "" {
			read = v.fg("failed", "not readable: "+cleanBoard(s.Err))
		}
		return append(out, v.kv("updated", read, w))
	}
	read := "read " + hostAgo(v.now.Sub(s.At)) + " · every 10 s while open"
	if s.Err != "" {
		read = v.fg("failed", "last read failed: "+cleanBoard(s.Err)) + v.fg("dim", " · showing "+hostAgo(v.now.Sub(s.At)))
	}
	out = append(out, v.kv("updated", read, w))
	if len(o.Tasks) == 0 {
		return append(out, "", v.fg("dim", "  the run has no tasks yet"))
	}
	g := newPlanGraph(o.Tasks)
	var sel store.PlanTask
	for _, t := range o.Tasks {
		if t.ID == m.Selected {
			sel = t
		}
	}

	// The sections under the graph: the selected task (title, four rows)
	// and the versions (title and its rows).
	versions := min(len(o.Versions), 4)
	below := 1 + 5 + 1 + 1 + max(1, versions)
	graphRows := max(3, h-len(out)-1-below)

	out = append(out, "")
	out = append(out, v.planGraphLines(g, sel, w, graphRows)...)

	out = append(out, "", v.title(cleanBoard(sel.ID), cleanBoard(sel.Title), false, w))
	st := v.planTaskState(sel)
	out = append(out, v.kv("state", v.fg(boardStateColor(st), v.glyph(st)+" "+string(st)), w))
	needs := "nothing"
	if len(sel.DependsOn) > 0 {
		needs = cleanBoard(strings.Join(sel.DependsOn, ", "))
	}
	unblocks := "nothing"
	if d := g.Down[sel.ID]; len(d) > 0 {
		unblocks = cleanBoard(strings.Join(d, ", "))
	}
	out = append(out, v.kv("needs", needs, w), v.kv("unblocks", unblocks, w))
	scope := "—"
	if len(sel.Scope) > 0 {
		scope = cleanBoard(strings.Join(sel.Scope, ", "))
	}
	out = append(out, v.kv("scope", v.fg("dim", scope), w))

	out = append(out, "", v.title("VERSIONS", "every change to an approved plan is an amendment the owner approves", false, w))
	if len(o.Versions) == 0 {
		out = append(out, v.fg("dim", PadCell("  no stored plan versions for this run", w)))
	}
	for _, pv := range o.Versions[:versions] {
		col := "dim"
		if pv.State == "APPROVED" {
			col = "merged"
		}
		summary := pv.Reason
		if summary == "" {
			summary = "drafted from the goal"
			if pv.Supersedes > 0 {
				summary = fmt.Sprintf("replaces v%d", pv.Supersedes)
			}
		}
		if pv.Current {
			summary += " · the run's plan"
		}
		out = append(out, PadCell("  "+v.fg("accent", PadCell(fmt.Sprintf("v%d", pv.Version), 4))+" "+v.fg("dim", PadCell(planClock(pv.At, v.now), 11))+" "+v.fg(col, PadCell(strings.ToLower(pv.State), 11))+v.fg("dim", " · "+cleanBoard(summary)), w))
	}
	return out
}

// planVersionHead names the run's plan version and when it was approved.
func planVersionHead(o store.PlanOverview, now time.Time) string {
	if o.PlanVersion == 0 {
		return ""
	}
	head := fmt.Sprintf("plan v%d", o.PlanVersion)
	for _, pv := range o.Versions {
		if pv.Current {
			head += " " + strings.ToLower(pv.State)
			if !pv.At.IsZero() {
				head += " " + planClock(pv.At, now)
			}
		}
	}
	return head
}

// planClock is a time of day, with the date when it is not today's.
func planClock(at, now time.Time) string {
	if at.IsZero() {
		return "—"
	}
	at = at.In(now.Location())
	if y, m, d := at.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return at.Format("15:04")
	}
	return at.Format("Jan 2 15:04")
}

// planGraphLines paints the graph: a column per level, each task its state
// glyph and id. The selected task is highlighted and what it needs and
// unblocks is in the accent colour. Columns that do not fit scroll to keep
// the selected task's level in view, and rows to keep it in the rows.
func (v boardVisual) planGraphLines(g planGraph, sel store.PlanTask, w, rows int) []string {
	cw := min(16, max(10, (w-2)/max(1, g.Levels)))
	fit := max(1, (w-2)/cw)
	first := 0
	if l := g.Level[sel.ID]; l >= fit {
		first = l - fit + 1
	}
	last := min(g.Levels, first+fit)
	cols := make([][]store.PlanTask, g.Levels)
	for _, t := range g.Order {
		cols[g.Level[t.ID]] = append(cols[g.Level[t.ID]], t)
	}
	height := 0
	selRow := 0
	for l := first; l < last; l++ {
		height = max(height, len(cols[l]))
		for i, t := range cols[l] {
			if t.ID == sel.ID {
				selRow = i
			}
		}
	}
	rows = max(1, rows-1)
	top := 0
	if selRow >= rows {
		top = selRow - rows + 1
	}
	related := map[string]bool{}
	for _, d := range sel.DependsOn {
		related[d] = true
	}
	for _, d := range g.Down[sel.ID] {
		related[d] = true
	}
	var head strings.Builder
	head.WriteString("  ")
	for l := first; l < last; l++ {
		head.WriteString(PadCell(fmt.Sprintf("level %d", l), cw))
	}
	more := []string{}
	if first > 0 {
		more = append(more, fmt.Sprintf("← %d", first))
	}
	if last < g.Levels {
		more = append(more, fmt.Sprintf("%d →", g.Levels-last))
	}
	if top > 0 || top+rows < height {
		more = append(more, fmt.Sprintf("rows %d–%d of %d", top+1, min(height, top+rows), height))
	}
	out := []string{v.fg("dim", PadCell(head.String(), w-VisibleLen(strings.Join(more, " · ")))+strings.Join(more, " · "))}
	for r := top; r < min(height, top+rows); r++ {
		var line strings.Builder
		line.WriteString("  ")
		for l := first; l < last; l++ {
			if r >= len(cols[l]) {
				line.WriteString(PadCell("", cw))
				continue
			}
			t := cols[l][r]
			st := v.planTaskState(t)
			id := cleanBoard(t.ID)
			cell := v.fg(boardStateColor(st), v.glyph(st)) + " "
			switch {
			case t.ID == sel.ID:
				cell = v.bg("sel", PadCell(cell+v.bold(v.fg("white", id)), cw-1)) + " "
			case related[t.ID]:
				cell = PadCell(cell+v.fg("accent", id), cw)
			default:
				cell = PadCell(cell+id, cw)
			}
			line.WriteString(cell)
		}
		out = append(out, PadCell(line.String(), w))
	}
	return out
}
