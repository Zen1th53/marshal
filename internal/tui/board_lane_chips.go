package tui

import (
	"fmt"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// laneChips is the lanes pane as one row of chips, as in the narrow mockup:
// "LANES ⣿codex-1 ⣿claude-1 ⏸codex-3 +2", each chip its state's glyph and
// colour, and "+N" for the lanes that do not fit.
func (v boardVisual) laneChips(p layout.Pane) string {
	w := p.Rect.W
	head := "LANES"
	color := "white"
	if v.focus == layout.Lanes {
		head, color = "> LANES", "accent"
	}
	line, used := v.bold(v.fg(color, head)), VisibleLen(head)
	// When the named chips do not all fit, the lanes are one glyph each, as
	// mockup 06 draws them at 80 columns, and l lists them.
	need := used
	for _, l := range v.board.Lanes {
		need += 1 + VisibleLen(cleanBoard(l.Worker)) + 1
	}
	if need > w {
		line += " "
		used++
		for _, l := range v.board.Lanes {
			if used+1 > w-9 {
				break
			}
			st := model.TaskState(l.State)
			g := v.glyph(st)
			if l.State == model.LaneIdle {
				g = "○"
				if !v.unicode() {
					g = "o"
				}
			}
			v.hit(boardHit{row: p.Rect.Y, x0: p.Rect.X + used, x1: p.Rect.X + used + 1, kind: hitLane, id: l.Worker})
			line += v.fg(boardStateColor(st), g)
			used++
		}
		return PadCell(line+v.fg("dim", "  l: list"), w)
	}
	for i, l := range v.board.Lanes {
		st := model.TaskState(l.State)
		g := v.glyph(st)
		if l.State == model.LaneIdle {
			g = "○"
			if !v.unicode() {
				g = "o"
			}
		}
		chip := " " + g + cleanBoard(l.Worker)
		more := ""
		if rest := len(v.board.Lanes) - i - 1; rest > 0 {
			more = fmt.Sprintf(" +%d", rest)
		}
		if used+VisibleLen(chip)+VisibleLen(more) > w {
			line += v.fg("dim", fmt.Sprintf(" +%d", len(v.board.Lanes)-i))
			break
		}
		v.hit(boardHit{row: p.Rect.Y, x0: p.Rect.X + used, x1: p.Rect.X + used + VisibleLen(chip), kind: hitLane, id: l.Worker})
		c := v.fg(boardStateColor(st), chip)
		if v.selectedLane != "" && l.Worker == v.selectedLane {
			c = v.bg("sel", c)
		}
		line += c
		used += VisibleLen(chip)
	}
	return PadCell(line, w)
}
