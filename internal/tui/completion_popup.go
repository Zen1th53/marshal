package tui

import (
	"fmt"
	"strings"
)

// maxPopupRows bounds the completion list so it never crowds out the workspace.
const maxPopupRows = 8

// renderCompletionPopup draws the candidate list as rows of the current frame.
//
// It returns lines, not terminal output. The screen model places them, so
// opening, cycling and closing the popup all repaint in place and never add to
// scrollback — which is what pressing Tab repeatedly used to do.
func renderCompletionPopup(matches []string, selected int, th *Theme, cols int) []string {
	if len(matches) == 0 {
		return nil
	}
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}

	// Window the list around the selection so a long list stays navigable.
	start := 0
	if selected >= maxPopupRows {
		start = selected - maxPopupRows + 1
	}
	end := start + maxPopupRows
	if end > len(matches) {
		end = len(matches)
	}

	widest := 0
	for _, m := range matches[start:end] {
		if l := VisibleLen(m) + describeWidth(m); l > widest {
			widest = l
		}
	}
	boxWidth := widest + 6
	if boxWidth > cols-4 {
		boxWidth = cols - 4
	}
	if boxWidth < 24 {
		boxWidth = 24
	}

	inner := boxWidth - 2
	var out []string
	out = append(out, "  "+th.Colorize(th.Muted,
		th.BoxTopLeft+strings.Repeat(th.BoxHoriz, inner)+th.BoxTopRight))

	for i := start; i < end; i++ {
		label := matches[i]
		desc := describeCompletion(label)

		row := fmt.Sprintf(" %s %s", PadCell(label, 14), th.Colorize(th.Muted, desc))
		row = PadCell(row, inner)
		if i == selected {
			row = th.Colorize(th.Reverse, row)
		}
		out = append(out, "  "+th.Colorize(th.Muted, th.BoxVert)+row+th.Colorize(th.Muted, th.BoxVert))
	}

	hint := fmt.Sprintf(" %d/%d · Tab next · ↑↓ select · Enter accept · Esc close",
		selected+1, len(matches))
	out = append(out, "  "+th.Colorize(th.Muted,
		th.BoxBottomLeft+strings.Repeat(th.BoxHoriz, inner)+th.BoxBottomRight))
	out = append(out, "  "+th.Colorize(th.Muted, Truncate(hint, cols-4)))

	return out
}

// describeCompletion looks up a one-line purpose for a candidate from the
// capability registry, so the list explains itself rather than requiring the
// operator to already know every command.
func describeCompletion(candidate string) string {
	if !strings.HasPrefix(candidate, "/") {
		return ""
	}
	for _, cap := range GlobalRegistry.All() {
		surface := strings.TrimSpace(cap.TUISurface)
		if surface == "" {
			continue
		}
		if strings.Fields(surface)[0] == candidate {
			return cap.Name
		}
	}
	return ""
}

func describeWidth(candidate string) int {
	return VisibleLen(describeCompletion(candidate)) + 2
}
