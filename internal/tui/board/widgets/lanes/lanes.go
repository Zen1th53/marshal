// Package lanes renders the Board's live worker invocation rows.
// Each live worker gets one row: spinner, worker, task, elapsed time,
// 8-cell token sparkline, tokens, and last action (redacted).
// The widget adapts across three densities: full, compact, and strip.
package lanes

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/redaction"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// SparkBarGlyphs are the 8 vertical level bars for sparklines.
var (
	UnicodeSparkBars = [8]string{" ", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	ASCIISparkBars   = [8]string{"_", ".", "-", "=", "+", "*", "#", "#"}
)

// SpinnerGlyphs returns braille or ascii spinner by utf8 flag.
var (
	BrailleSpinners = [...]string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}
	ASCIISpinners   = [...]string{"|", "/", "-", "\\"}
)

// State glyphs for strip density.
const (
	GlyphWorking = "⣾"
	GlyphParked  = "▮"
	GlyphRework  = "↻"
	GlyphDone    = "✓"
	GlyphIdle    = "○"
)

// State glyphs ASCII fallback.
const (
	ASCIIWorking = "*"
	ASCIIParked  = "#"
	ASCIIRework  = "@"
	ASCIIDone    = "v"
	ASCIIIdle    = "."
)

// Options tunes lanes rendering.
type Options struct {
	Now        time.Time
	FrameTick  uint64
	UTF8       bool
	Selected   int // -1 when none selected
	OpenPaneID string
}

// Widget manages the lanes pane view state.
type Widget struct {
	selected int
}

// NewWidget returns an initialized lanes widget.
func NewWidget() *Widget {
	return &Widget{selected: 0}
}

// Selected returns the currently selected lane index.
func (w *Widget) Selected() int {
	return w.selected
}

// SetSelected updates the selected lane index.
func (w *Widget) SetSelected(idx, total int) {
	if total <= 0 {
		w.selected = 0
		return
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= total {
		idx = total - 1
	}
	w.selected = idx
}

// Move moves the selection by delta (-1 or +1).
func (w *Widget) Move(delta int, total int) {
	w.SetSelected(w.selected+delta, total)
}

// SelectedLane returns the selected lane from the board, or false if empty.
func (w *Widget) SelectedLane(b model.Board) (model.Lane, bool) {
	if len(b.Lanes) == 0 {
		return model.Lane{}, false
	}
	w.SetSelected(w.selected, len(b.Lanes))
	return b.Lanes[w.selected], true
}

// Render renders the lanes pane lines matching the pane geometry and density.
func Render(p layout.Pane, b model.Board, opt Options) []string {
	if p.Rect.H <= 0 || p.Rect.W <= 0 {
		return nil
	}

	switch p.Density {
	case layout.Strip:
		return []string{renderStrip(p.Rect.W, b.Lanes, opt)}
	case layout.Compact:
		return renderRows(p.Rect.W, p.Rect.H, b.Lanes, opt, true)
	default:
		return renderRows(p.Rect.W, p.Rect.H, b.Lanes, opt, false)
	}
}

// renderStrip renders a single line summary of lanes.
// Format: "LANES 9⣾ 2⏸ 1⟳ 12✓"
func renderStrip(width int, lanes []model.Lane, opt Options) string {
	counts := map[model.LaneState]int{}
	for _, l := range lanes {
		counts[l.State]++
	}

	var parts []string
	parts = append(parts, "LANES")

	if opt.UTF8 {
		if c := counts[model.LaneWorking]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", c, GlyphWorking))
		}
		if c := counts[model.LaneParked]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", c, GlyphParked))
		}
		if c := counts[model.LaneRework]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", c, GlyphRework))
		}
		if c := counts[model.LaneReview]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", c, GlyphWorking))
		}
		if c := counts[model.LaneIdle]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", c, GlyphIdle))
		}
	} else {
		if c := counts[model.LaneWorking]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", c, ASCIIWorking))
		}
		if c := counts[model.LaneParked]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", c, ASCIIParked))
		}
		if c := counts[model.LaneRework]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", c, ASCIIRework))
		}
		if c := counts[model.LaneReview]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", c, ASCIIWorking))
		}
		if c := counts[model.LaneIdle]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", c, ASCIIIdle))
		}
	}

	if len(parts) == 1 {
		parts = append(parts, "0")
	}

	line := strings.Join(parts, " ")
	return pad(line, width)
}

// renderRows renders one row per lane up to height rows.
func renderRows(width, height int, lanes []model.Lane, opt Options, compact bool) []string {
	if len(lanes) == 0 {
		return []string{pad("No active worker lanes.", width)}
	}

	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}

	total := len(lanes)
	maxRows := height
	showMore := total > maxRows && maxRows > 1
	visibleCount := total
	if showMore {
		visibleCount = maxRows - 1
	} else if visibleCount > maxRows {
		visibleCount = maxRows
	}

	lines := make([]string, 0, height)
	for i := 0; i < visibleCount; i++ {
		l := lanes[i]
		row := formatLaneRow(l, width, now, opt, compact)
		lines = append(lines, row)
	}

	if showMore {
		remaining := total - visibleCount
		lines = append(lines, pad(fmt.Sprintf("… %d more", remaining), width))
	}

	return lines
}

// formatLaneRow formats a single lane row.
// Full layout:
//
//	<spinner> <worker:8> <task:6> <elapsed:5> <spark:8> <tokens:5> <action>
//
// Compact layout (narrower):
//
//	<spinner> <worker:8> <task:6> <elapsed:5> <spark:8> <tokens:5> <action>
//
// An invariant: a lane row never exceeds one line.
func formatLaneRow(l model.Lane, width int, now time.Time, opt Options, compact bool) string {
	spinner := spinnerGlyph(l.State, opt.FrameTick, opt.UTF8)

	worker := l.Worker
	if len(worker) > 8 {
		worker = worker[:8]
	}

	taskID := l.TaskID
	if taskID == "" {
		taskID = "-"
	}
	if len(taskID) > 6 {
		taskID = taskID[:6]
	}

	elapsed := formatElapsed(l.Started, now)
	spark := formatSparkline(l.Spark(), opt.UTF8)
	tokens := formatTokens(l.Tokens)

	// Invariant: Last action passes the existing redaction before display
	action := redaction.RedactContent(l.Last, nil)
	action = strings.ReplaceAll(action, "\n", " ")
	action = strings.TrimSpace(action)

	prefix := fmt.Sprintf("%s %-8s %-6s %5s %s %5s",
		spinner,
		worker,
		taskID,
		elapsed,
		spark,
		tokens,
	)

	prefixWidth := visibleLen(prefix)
	avail := width - prefixWidth - 1 // 1 space before action
	if avail <= 0 {
		return truncateOrPad(prefix, width)
	}

	if action == "" {
		return pad(prefix, width)
	}

	if visibleLen(action) > avail {
		action = truncate(action, avail)
	}

	return pad(prefix+" "+action, width)
}

func spinnerGlyph(state model.LaneState, tick uint64, isUTF8 bool) string {
	if state == model.LaneIdle {
		if isUTF8 {
			return GlyphIdle
		}
		return ASCIIIdle
	}
	if state == model.LaneParked {
		if isUTF8 {
			return GlyphParked
		}
		return ASCIIParked
	}
	if isUTF8 {
		return BrailleSpinners[tick%uint64(len(BrailleSpinners))]
	}
	return ASCIISpinners[tick%uint64(len(ASCIISpinners))]
}

func formatElapsed(started, now time.Time) string {
	if started.IsZero() {
		return "  0s"
	}
	d := now.Sub(started)
	if d < 0 {
		return "  0s"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%3ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%3dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%3dh", int(d.Hours()))
	}
}

func formatSparkline(spark [8]uint8, isUTF8 bool) string {
	var b strings.Builder
	for _, v := range spark {
		lvl := int(v)
		if lvl < 0 {
			lvl = 0
		}
		if lvl > 7 {
			lvl = 7
		}
		if isUTF8 {
			b.WriteString(UnicodeSparkBars[lvl])
		} else {
			b.WriteString(ASCIISparkBars[lvl])
		}
	}
	return b.String()
}

func formatTokens(tokens int64) string {
	switch {
	case tokens <= 0:
		return "    0"
	case tokens < 1000:
		return fmt.Sprintf("%5d", tokens)
	case tokens < 1_000_000:
		return fmt.Sprintf("%4dk", tokens/1000)
	default:
		return fmt.Sprintf("%4dM", tokens/1_000_000)
	}
}

func pad(s string, width int) string {
	w := visibleLen(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func truncate(s string, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	cur := 0
	var out []rune
	for _, r := range s {
		rw := runeWidth(r)
		if cur+rw > maxCols {
			break
		}
		out = append(out, r)
		cur += rw
	}
	return string(out)
}

func truncateOrPad(s string, width int) string {
	w := visibleLen(s)
	if w > width {
		return truncate(s, width)
	}
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

func visibleLen(s string) int {
	count := 0
	inEsc := false
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			inEsc = true
			i++
			continue
		}
		if inEsc {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEsc = false
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		count += runeWidth(r)
		i += size
	}
	return count
}

func runeWidth(r rune) int {
	// Fast-path ASCII printable
	if r >= 32 && r < 127 {
		return 1
	}
	// Common sparkline and braille unicode chars are 1 cell
	if r >= 0x2580 && r <= 0x259F { // Block elements (e.g.  ▂▃▄▅▆▇█)
		return 1
	}
	if r >= 0x2800 && r <= 0x28FF { // Braille patterns (e.g. ⣾⣽...)
		return 1
	}
	// Wide characters (CJK, emojis)
	if r >= 0x1100 && (r <= 0x115f ||
		r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1f300 && r <= 0x1f64f) ||
		(r >= 0x1f900 && r <= 0x1f9ff) ||
		(r >= 0x20000 && r <= 0x2fffd) ||
		(r >= 0x30000 && r <= 0x3fffd)) {
		return 2
	}
	return 1
}
