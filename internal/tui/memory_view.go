package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// memoryPane is the Memory view's one pane: the Board's body, between its
// header and footer. The Marshal chat stays docked beside it.
const memoryPane layout.PaneID = "memory"

// memorySnapshot is the Memory view's data: the last overview read and
// what the view knows around it.
type memorySnapshot struct {
	Overview store.MemoryOverview
	// Loaded is set once a read has succeeded; At is when the last read
	// finished and Err why it failed, if it did.
	Loaded bool
	At     time.Time
	Err    string
	// MCP is the MCP server's state; empty when nothing reports it.
	MCP string
	// StorePath is where the memory lives, as the owner would type it.
	StorePath string
}

// memoryPaint is what one frame of the Memory view needs.
type memoryPaint struct {
	Snap memorySnapshot
	// Selected is the candidate the review keys act on.
	Selected string
	// Pending is 'a' or 'r' while that decision waits for Enter.
	Pending rune
	// Busy is the candidate whose decision is being recorded.
	Busy string
}

func memoryStorePath() string { return statepath.DirName + "/state.db" }

// lifeCount sums one lifecycle over every kind.
func lifeCount(c store.MemoryOverviewCounts, life string) int {
	n := 0
	for _, byLife := range c.ByKindLife {
		n += byLife[life]
	}
	return n
}

// memoryRender paints the Memory view into a w×h body, scrolled down by
// scroll lines; follow scrolls the selected candidate into view. It returns
// the lines, the scroll it painted at and the furthest useful scroll.
func (v boardVisual) memoryRender(m memoryPaint, w, h, scroll int, follow bool) ([]string, int, int) {
	all, selAt := v.memoryLines(m, w)
	maxScroll := max(0, len(all)-h)
	if follow && selAt >= 0 && h > 0 {
		// Keep the selected candidate, its title and its keys in view.
		scroll = min(max(scroll, selAt+3-h), selAt)
	}
	scroll = min(max(0, scroll), maxScroll)
	out := slices.Clone(all[scroll:min(len(all), scroll+h)])
	if scroll < maxScroll && len(out) == h && h > 0 {
		out[h-1] = v.fg("dim", fmt.Sprintf("… %d more lines · PgDn scroll", maxScroll-scroll))
	}
	for i := range out {
		out[i] = PadCell(out[i], w)
	}
	if !v.unicode() {
		out = hostASCII(out, w)
	}
	return out, scroll, maxScroll
}

// memoryLines lays the sections out as the prototype does: MEMORY across
// the top, then REVIEW QUEUE beside BY EDITION and RECENT RECALLS beside
// MCP TOOLS when there is room for two columns. It also returns the line
// the selected candidate starts on, or -1.
func (v boardVisual) memoryLines(m memoryPaint, w int) ([]string, int) {
	var out []string
	selAt := -1
	add := func(lines []string, sel int) {
		if len(out) > 0 {
			out = append(out, "")
		}
		if sel >= 0 {
			selAt = len(out) + sel
		}
		out = append(out, lines...)
	}
	add(v.memTop(m, w), -1)
	if w >= layout.TwoColumns {
		lw := (w - 2) / 2
		rw := w - 2 - lw
		queue, sel := v.memQueue(m, lw)
		add(sideBySide(queue, v.memEditions(m, rw), lw, rw), sel)
		add(sideBySide(v.memRecalls(m, lw), v.memTools(m, rw), lw, rw), -1)
	} else {
		queue, sel := v.memQueue(m, w)
		add(queue, sel)
		add(v.memEditions(m, w), -1)
		add(v.memRecalls(m, w), -1)
		add(v.memTools(m, w), -1)
	}
	return out, selAt
}

func sideBySide(left, right []string, lw, rw int) []string {
	rows := make([]string, max(len(left), len(right)))
	for i := range rows {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		rows[i] = PadCell(l, lw) + "  " + PadCell(r, rw)
	}
	return rows
}

func (v boardVisual) memTop(m memoryPaint, w int) []string {
	s := m.Snap
	o := s.Overview
	if !s.Loaded {
		summary := "reading the store…"
		if s.Err != "" {
			summary = v.fg("failed", "not readable: "+cleanBoard(s.Err))
		}
		return []string{v.title("MEMORY", summary, false, w), v.kv("store", firstNonEmpty(s.StorePath, memoryStorePath()), w)}
	}
	waiting := lifeCount(o.Counts, "candidate")
	summary := fmt.Sprintf("%s records · %s durable · %s waiting review · %s rejected", commas(int64(o.Counts.Total)), commas(int64(lifeCount(o.Counts, "durable"))), commas(int64(waiting)), commas(int64(lifeCount(o.Counts, "rejected"))))
	if s.MCP != "" {
		summary += " · MCP server " + s.MCP
	}
	out := []string{v.title("MEMORY", summary, false, w)}
	out = append(out, v.kv("store", firstNonEmpty(s.StorePath, memoryStorePath())+" · project memory shared by every window", w))
	recalls := commas(int64(o.RecentRecalls.TotalReceipts)) + " served"
	if len(o.RecentRecalls.Recent) > 0 && !o.RecentRecalls.Recent[0].CreatedAt.IsZero() {
		recalls += " · last " + hostAgo(v.now.Sub(o.RecentRecalls.Recent[0].CreatedAt))
	}
	out = append(out, v.kv("recalls", recalls, w))
	out = append(out, v.kv("MCP", fmt.Sprintf("%s candidates from workers · %s proposals audited", commas(int64(o.MCPTools.CandidatesCount)), commas(int64(o.MCPTools.ProposalsCount))), w))
	snaps := "none yet"
	if n := o.Snapshots.TotalSnapshots; n > 0 {
		snaps = fmt.Sprintf("%d restore points", n)
		if o.Snapshots.NewestSnapshot != nil {
			snaps += " · last " + hostAgo(v.now.Sub(*o.Snapshots.NewestSnapshot))
		}
	}
	out = append(out, v.kv("snapshots", snaps, w))
	read := "read " + hostAgo(v.now.Sub(s.At)) + " · every 10 s while open"
	if s.Err != "" {
		read = v.fg("failed", "last read failed: "+cleanBoard(s.Err)) + v.fg("dim", " · showing "+hostAgo(v.now.Sub(s.At)))
	}
	out = append(out, v.kv("updated", read, w))
	return out
}

// memQueue is the candidates waiting review; the selected one carries its
// keys, or the decision waiting for Enter. It returns the line the
// selected candidate starts on, or -1.
func (v boardVisual) memQueue(m memoryPaint, w int) ([]string, int) {
	o := m.Snap.Overview
	waiting := max(lifeCount(o.Counts, "candidate"), len(o.ReviewQueue))
	summary := "workers proposed these; nothing becomes durable without review"
	if VisibleLen("REVIEW QUEUE "+summary) > w {
		summary = "nothing becomes durable without review"
	}
	switch {
	case len(o.ReviewQueue) == 0:
		summary = "empty"
	case waiting > len(o.ReviewQueue):
		summary = fmt.Sprintf("newest %d of %d · none durable without review", len(o.ReviewQueue), waiting)
	}
	out := []string{v.title("REVIEW QUEUE", summary, false, w)}
	if len(o.ReviewQueue) == 0 {
		out = append(out, v.fg("dim", PadCell("  nothing waiting review", w)))
		return out, -1
	}
	sel := -1
	for _, it := range o.ReviewQueue {
		selected := it.ID == m.Selected
		meta := []string{string(it.Kind)}
		if it.Confidence != "" {
			meta = append(meta, "confidence "+string(it.Confidence))
		}
		if it.Authority != "" {
			meta = append(meta, string(it.Authority))
		}
		if it.CreatedAt != nil {
			meta = append(meta, hostAgo(v.now.Sub(*it.CreatedAt)))
		}
		head := v.fg("waiting", "?") + " " + v.fg("accent", cleanBoard(it.ID)) + " " + v.fg("dim", strings.Join(meta, " · "))
		title := "  " + cleanBoard(it.Title)
		lines := []string{PadCell(head, w), PadCell(title, w)}
		if selected {
			sel = len(out)
			var keys string
			switch {
			case m.Busy == it.ID:
				keys = v.fg("waiting", "recording the decision…")
			case m.Pending == 'a':
				keys = v.fg("waiting", "Enter to keep "+cleanBoard(it.ID)+" as durable · Esc cancels")
			case m.Pending == 'r':
				keys = v.fg("waiting", "Enter to reject "+cleanBoard(it.ID)+" · Esc cancels")
			default:
				keys = v.bg("rule", "[a]") + " keep as durable  " + v.bg("rule", "[r]") + " reject  " + v.bg("rule", "[i]") + " evidence"
			}
			lines = append(lines, PadCell("  "+keys, w))
			for i := range lines {
				lines[i] = v.bg("sel", PadCell(lines[i], w))
			}
		}
		out = append(out, lines...)
	}
	return out, sel
}

func (v boardVisual) memEditions(m memoryPaint, w int) []string {
	e := m.Snap.Overview.EditionTags
	out := []string{v.title("BY EDITION", "records tagged [edition/area]", false, w)}
	rows := []struct {
		name  string
		n     int
		areas []string
	}{{"Marshal", e.ExtremeCount, e.ExtremeAreas}, {"community", e.CommunityCount, e.CommunityAreas}, {"shared", e.SharedCount, e.SharedAreas}}
	total := e.ExtremeCount + e.CommunityCount + e.SharedCount
	for _, r := range rows {
		suffix := fmt.Sprintf("%3d", r.n)
		areas := slices.Clone(r.areas)
		slices.Sort(areas)
		label := PadCell(r.name, 10)
		bw := max(4, min(16, w-VisibleLen(label)-VisibleLen(suffix)-2))
		line := label + " " + v.bar(float64(r.n), float64(max(1, total)), "verify", nil, bw) + " " + suffix
		if len(areas) > 0 {
			// Areas only when they fit whole: a cut name reads as another.
			room := w - VisibleLen(line) - 3
			var shown []string
			for i, a := range areas {
				more := ""
				if rest := len(areas) - i - 1; rest > 0 {
					more = fmt.Sprintf(" +%d", rest)
				}
				if VisibleLen(strings.Join(append(slices.Clone(shown), cleanBoard(a)), ", ")+more) > room {
					break
				}
				shown = append(shown, cleanBoard(a))
			}
			switch {
			case len(shown) > 0 && len(shown) < len(areas):
				line += " · " + v.fg("dim", strings.Join(shown, ", ")+fmt.Sprintf(" +%d", len(areas)-len(shown)))
			case len(shown) > 0:
				line += " · " + v.fg("dim", strings.Join(shown, ", "))
			case room >= 8:
				line += " · " + v.fg("dim", fmt.Sprintf("%d areas", len(areas)))
			}
		}
		out = append(out, PadCell(line, w))
	}
	if total == 0 {
		out = append(out, v.fg("dim", PadCell("  no tagged records yet", w)))
	}
	return out
}

func (v boardVisual) memRecalls(m memoryPaint, w int) []string {
	r := m.Snap.Overview.RecentRecalls
	out := []string{v.title("RECENT RECALLS", "who asked memory for what", false, w)}
	for _, rc := range r.Recent[:min(8, len(r.Recent))] {
		who := firstNonEmpty(cleanBoard(rc.CallerID), cleanBoard(rc.Provider), "—")
		task := firstNonEmpty(cleanBoard(rc.TaskID), cleanBoard(rc.RunID), "—")
		query := cleanBoard(rc.QueryDigest)
		if len(query) > 8 {
			query = query[:8]
		}
		outcome := firstNonEmpty(cleanBoard(rc.OutcomeStatus), "served")
		col := "dim"
		if rc.DeniedCount > 0 {
			outcome += fmt.Sprintf(" · %d denied", rc.DeniedCount)
			col = "waiting"
		}
		ago := ""
		if !rc.CreatedAt.IsZero() {
			ago = hostAgo(v.now.Sub(rc.CreatedAt))
		}
		line := v.fg("accent", PadCell(who, 10)) + " " + v.fg("dim", PadCell(task, 8)) + " " + PadCell(query, 8) + " " + v.fg(col, "→ "+outcome)
		out = append(out, PadCell(PadCell(line, max(0, w-10))+v.fg("dim", fmt.Sprintf("%10s", ago)), w))
	}
	if len(r.Recent) == 0 {
		out = append(out, v.fg("dim", PadCell("  no recalls yet", w)))
	}
	return out
}

func (v boardVisual) memTools(m memoryPaint, w int) []string {
	calls := m.Snap.Overview.MCPTools.ToolCalls
	out := []string{v.title("MCP TOOLS", "calls since the store was created", false, w)}
	type tool struct {
		name string
		n    int
	}
	var tools []tool
	for name, n := range calls {
		tools = append(tools, tool{name, n})
	}
	slices.SortFunc(tools, func(a, b tool) int { return cmp.Or(b.n-a.n, strings.Compare(a.name, b.name)) })
	nw := max(8, min(28, w-12))
	out = append(out, v.fg("dim", PadCell("  "+PadCell("tool", nw)+fmt.Sprintf("%8s", "calls"), w)))
	for _, t := range tools[:min(10, len(tools))] {
		out = append(out, PadCell("  "+PadCell(cleanBoard(t.name), nw)+fmt.Sprintf("%8s", commas(int64(t.n))), w))
	}
	if len(tools) > 10 {
		out = append(out, v.fg("dim", PadCell(fmt.Sprintf("  … %d more tools", len(tools)-10), w)))
	}
	if len(tools) == 0 {
		out = append(out, v.fg("dim", PadCell("  no gateway calls recorded", w)))
	}
	return out
}
