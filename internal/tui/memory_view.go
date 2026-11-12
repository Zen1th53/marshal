package tui

import (
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
	// MCP is why the MCP connection last failed, in one plain sentence;
	// empty while it works, and then the view says nothing about MCP.
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
		out[h-1] = v.fg("dim", "… "+count(maxScroll-scroll, "more line", "more lines")+" · PgDn scroll")
	}
	for i := range out {
		out[i] = PadCell(out[i], w)
	}
	if !v.unicode() {
		out = hostASCII(out, w)
	}
	return out, scroll, maxScroll
}

// memoryLines lays the sections out: MEMORY across the top, then REVIEW QUEUE. It also returns the
// line the selected candidate starts on, or -1. The MCP machinery behind memory (tool calls,
// callers, query digests) is not shown; an MCP line appears in MEMORY only when MCP failed.
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
	queue, sel := v.memQueue(m, w)
	add(queue, sel)
	return out, selAt
}

func (v boardVisual) memTop(m memoryPaint, w int) []string {
	s := m.Snap
	o := s.Overview
	if !s.Loaded {
		summary := "reading the store…"
		if s.Err != "" {
			summary = viewLoadFailed
		}
		return []string{v.title("MEMORY", summary, false, w), v.kv("store", firstNonEmpty(s.StorePath, memoryStorePath()), w)}
	}
	waiting := lifeCount(o.Counts, "candidate")
	summary := fmt.Sprintf("%s %s · %s durable · %s waiting review · %s rejected", commas(int64(o.Counts.Total)), plural(o.Counts.Total, "record", "records"), commas(int64(lifeCount(o.Counts, "durable"))), commas(int64(waiting)), commas(int64(lifeCount(o.Counts, "rejected"))))
	out := []string{v.title("MEMORY", summary, false, w)}
	out = append(out, v.kv("store", firstNonEmpty(s.StorePath, memoryStorePath())+" · project memory shared by every window", w))
	recalls := commas(int64(o.RecentRecalls.TotalReceipts)) + " served"
	if len(o.RecentRecalls.Recent) > 0 && !o.RecentRecalls.Recent[0].CreatedAt.IsZero() {
		recalls += " · last " + hostAgo(v.now.Sub(o.RecentRecalls.Recent[0].CreatedAt))
	}
	out = append(out, v.kv("recalls", recalls, w))
	if s.MCP != "" {
		out = append(out, v.kv("MCP", v.fg("waiting", cleanBoard(s.MCP)), w))
	}
	snaps := "none yet"
	if n := o.Snapshots.TotalSnapshots; n > 0 {
		snaps = count(n, "restore point", "restore points")
		if o.Snapshots.NewestSnapshot != nil {
			snaps += " · last " + hostAgo(v.now.Sub(*o.Snapshots.NewestSnapshot))
		}
	}
	out = append(out, v.kv("snapshots", snaps, w))
	read := "read " + hostAgo(v.now.Sub(s.At)) + " · every 10 s while open"
	if s.Err != "" {
		read = "Couldn't refresh; showing what was read " + hostAgo(v.now.Sub(s.At)) + "."
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
