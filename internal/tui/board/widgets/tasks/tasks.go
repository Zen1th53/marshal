// Package tasks paints the Board's task projection in one pane.
package tasks

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/rivo/uniseg"
)

// Widget owns only navigation state. The caller replaces rows on each projection.
type Widget struct {
	SelectedID string
	Scroll     int
}

// Update keeps selection by canonical ID as rows change order.
func (w *Widget) Update(rows []model.TaskRow) {
	if len(rows) == 0 {
		w.SelectedID, w.Scroll = "", 0
		return
	}
	if slices.IndexFunc(rows, func(r model.TaskRow) bool { return r.ID == w.SelectedID }) < 0 {
		w.SelectedID = rows[0].ID
		w.Scroll = 0
	}
}

// Move selects the next row without acting on it.
func (w *Widget) Move(rows []model.TaskRow, delta int) {
	w.Update(rows)
	if len(rows) == 0 {
		return
	}
	i := slices.IndexFunc(rows, func(r model.TaskRow) bool { return r.ID == w.SelectedID })
	w.SelectedID = rows[max(0, min(len(rows)-1, i+delta))].ID
}

// Inspect is the task ID Enter hands to the inspector.
func (w Widget) Inspect() string { return w.SelectedID }

// Render returns at most height lines of no more than width terminal cells.
// The caller passes the animator's Frame after reading it under the workspace lock.
func (w *Widget) Render(rows []model.TaskRow, width, height int, density layout.Density, now time.Time, frame anim.Frame, color bool) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	w.Update(rows)
	if density == layout.Strip || height == 1 || width < layout.MinTasks {
		return []string{pad(strip(rows, color), width)}
	}
	out := []string{pad(fmt.Sprintf(" Tasks  %d", len(rows)), width)}
	slots := height - 1
	if slots == 0 {
		return out
	}
	selected := slices.IndexFunc(rows, func(r model.TaskRow) bool { return r.ID == w.SelectedID })
	if selected < w.Scroll {
		w.Scroll = selected
	}
	if selected >= w.Scroll+slots {
		w.Scroll = selected - slots + 1
	}
	w.Scroll = max(0, min(w.Scroll, max(0, len(rows)-slots)))
	visible := min(slots, len(rows)-w.Scroll)
	if len(rows)-w.Scroll > slots {
		visible = max(0, slots-1)
	}
	for _, row := range rows[w.Scroll : w.Scroll+visible] {
		line := renderRow(row, width, density, now, frame, color, row.ID == w.SelectedID)
		if row.ID == w.SelectedID && color {
			line = "\x1b[7m" + line + "\x1b[0m"
		}
		if frame.Pulsing(row.ID) && color {
			line = "\x1b[1m" + line + "\x1b[0m"
		}
		out = append(out, line)
	}
	if hidden := len(rows) - w.Scroll - visible; hidden > 0 {
		out = append(out, pad(fmt.Sprintf(" … %d more", hidden), width))
	}
	return out
}

func renderRow(r model.TaskRow, width int, density layout.Density, now time.Time, frame anim.Frame, color, selected bool) string {
	stateGlyph, shade := stateStyle(r.State, color)
	if r.State == model.TaskWorking || r.State == model.TaskReview {
		stateGlyph = frame.Spinner(color)
	}
	mark := "  "
	if selected {
		mark = glyph("› ", "> ", color)
	}
	id := fit(r.ID, 8)
	worker := fit(r.Worker, 12)
	elapsed := "--"
	if !r.Since.IsZero() {
		elapsed = duration(now.Sub(r.Since))
	}
	evidence := "--"
	if r.EvidenceKnown {
		evidence = fmt.Sprint(r.Evidence)
	}
	tail := fmt.Sprintf(" %5s  E:%-3s  R:%d/%d", elapsed, evidence, r.Returns, r.ReworkLimit)
	if density == layout.Compact {
		tail = fmt.Sprintf(" E:%s R:%d/%d", evidence, r.Returns, r.ReworkLimit)
	}
	state := paint(fit(stateGlyph+" "+string(r.State), 14), shade, color)
	prefix := mark + id + " " + state + " " + worker + " "
	scopeWidth := width - uniseg.StringWidth(stripANSI(prefix)) - uniseg.StringWidth(tail)
	if scopeWidth < 0 {
		return pad(prefix+tail, width)
	}
	scope := leftFit(strings.Join(r.Scope, ","), scopeWidth)
	return pad(prefix+scope+strings.Repeat(" ", max(0, scopeWidth-uniseg.StringWidth(scope)))+tail, width)
}

func stateStyle(state model.TaskState, unicode bool) (string, string) {
	switch state {
	case model.TaskParked:
		return glyph("▮", "!", unicode), "33"
	case model.TaskRework:
		return glyph("↻", "~", unicode), "33"
	case model.TaskReview:
		return glyph("◐", "*", unicode), "35"
	case model.TaskAccepted:
		return glyph("✓", "+", unicode), "32"
	case model.TaskWorking:
		return glyph("⣾", "*", unicode), "34"
	case model.TaskQueued:
		return glyph("·", "o", unicode), "33"
	case model.TaskMerged:
		return glyph("✓", "+", unicode), "32"
	case model.TaskCancelled:
		return glyph("×", "x", unicode), "31"
	default:
		panic("tasks: unknown task state " + string(state))
	}
}

func strip(rows []model.TaskRow, color bool) string {
	counts := map[model.TaskState]int{}
	for _, r := range rows {
		counts[r.State]++
	}
	return fmt.Sprintf(" Tasks %d  %d %s  %d %s  %d %s  %d %s", len(rows), counts[model.TaskWorking], paint(glyph("⣾", "*", color)+" working", "34", color), counts[model.TaskParked], paint(glyph("▮", "!", color)+" parked", "33", color), counts[model.TaskRework], paint(glyph("↻", "~", color)+" rework", "33", color), counts[model.TaskMerged], paint(glyph("✓", "+", color)+" merged", "32", color))
}

func glyph(utf, ascii string, unicode bool) string {
	if unicode {
		return utf
	}
	return ascii
}
func paint(s, shade string, color bool) string {
	if color {
		return "\x1b[" + shade + "m" + s + "\x1b[0m"
	}
	return s
}

func duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	plain := stripANSI(s)
	if uniseg.StringWidth(plain) > width {
		s = cut(s, width-1) + "…"
	}
	return s + strings.Repeat(" ", max(0, width-uniseg.StringWidth(stripANSI(s))))
}

func leftFit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if uniseg.StringWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	parts := []rune(s)
	for len(parts) > 0 && uniseg.StringWidth(string(parts)) > width-1 {
		parts = parts[1:]
	}
	return "…" + string(parts)
}

func cut(s string, width int) string {
	var b strings.Builder
	for _, r := range s {
		next := b.String() + string(r)
		if uniseg.StringWidth(stripANSI(next)) > width {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func pad(s string, width int) string {
	if uniseg.StringWidth(stripANSI(s)) > width {
		s = cut(stripANSI(s), width-1) + "…"
	}
	return s + strings.Repeat(" ", max(0, width-uniseg.StringWidth(stripANSI(s))))
}

func stripANSI(s string) string {
	var b strings.Builder
	escape := false
	for _, r := range s {
		if r == '\x1b' {
			escape = true
			continue
		}
		if escape {
			if r == 'm' {
				escape = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
