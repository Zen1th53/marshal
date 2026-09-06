package tui

import (
	"fmt"
	"strings"
)

// Screen is the workspace's frame buffer.
//
// The workspace composes a full frame of lines, hands it here, and this type
// writes only what changed, addressing each row absolutely. Two properties
// follow, and both were previously violated:
//
//   - A redraw never appends to scrollback. The old loop printed the prompt and
//     let the terminal scroll, so every keystroke left another copy of the
//     status banner behind.
//   - However many times state changes, exactly one statusline and one composer
//     exist on screen, because they are rows in a frame rather than output
//     events.
type Screen struct {
	term *Terminal
	prev []string
	rows int
	cols int
}

func NewScreen(term *Terminal) *Screen {
	return &Screen{term: term}
}

// Reset discards the diff baseline so the next Render repaints every row. Use it
// after a resize or after leaving an overlay that wrote outside the model.
func (s *Screen) Reset() {
	s.prev = nil
}

// Render paints the frame, writing only rows whose content changed, then places
// the hardware cursor at (cursorRow, cursorCol), both 1-indexed.
//
// The cursor is hidden for the duration of the paint so a partially drawn frame
// never flickers under it.
func (s *Screen) Render(lines []string, cols, rows int, cursorRow, cursorCol int) {
	if s.term == nil {
		return
	}
	if cols != s.cols || rows != s.rows {
		s.cols, s.rows = cols, rows
		s.prev = nil
	}

	// Clip to the terminal height; the frame owns the whole screen.
	if len(lines) > rows {
		lines = lines[:rows]
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}

	s.term.HideCursor()

	if s.prev == nil {
		s.term.ClearScreen()
		for i, line := range lines {
			s.term.CursorTo(i+1, 1)
			fmt.Fprint(s.term.out, line)
			s.term.ClearToEndOfLine()
		}
	} else {
		for i, line := range lines {
			if i < len(s.prev) && s.prev[i] == line {
				continue
			}
			s.term.CursorTo(i+1, 1)
			fmt.Fprint(s.term.out, line)
			s.term.ClearToEndOfLine()
		}
	}

	s.prev = append(s.prev[:0], lines...)

	s.term.CursorTo(cursorRow, cursorCol)
	s.term.ShowCursor()
}

// Frame is one composed workspace screen: a product header, a live activity
// viewport, one statusline and one composer, plus any overlay.
type Frame struct {
	Header          []string
	Body            []string
	Status          string
	Composer        []string
	Popup           []string
	ScrollOffset    int
	UnreadCount     int
	CursorRowOffset int
	CursorCol       int
}

// Lines lays the frame out for a terminal of the given size.
//
// The composer and statusline are pinned to the last rows so they hold still
// while the body scrolls, and the body is padded or clipped to fill exactly the
// space between. Returns the frame plus the row the cursor belongs on.
func (f Frame) Lines(cols, rows int) ([]string, int) {
	if rows < 4 {
		rows = 4
	}

	var out []string
	out = append(out, f.Header...)

	// Reserve the trailing rows: separator, statusline, composer lines.
	composerLines := f.Composer
	if len(composerLines) == 0 {
		composerLines = []string{""}
	}
	reserved := 2 + len(composerLines)
	popup := f.Popup
	if len(popup) > 0 {
		maxPopup := rows - len(f.Header) - reserved - 1
		if maxPopup < 0 {
			maxPopup = 0
		}
		if len(popup) > maxPopup {
			popup = popup[:maxPopup]
		}
		reserved += len(popup)
	}

	bodyHeight := rows - len(f.Header) - reserved
	if bodyHeight < 0 {
		bodyHeight = 0
	}

	body := f.Body
	if len(body) > bodyHeight {
		total := len(body)
		start := total - bodyHeight - f.ScrollOffset
		if start < 0 {
			start = 0
		}
		end := start + bodyHeight
		if end > total {
			end = total
		}
		body = body[start:end]
	}
	out = append(out, body...)
	for len(out) < len(f.Header)+bodyHeight {
		out = append(out, "")
	}

	out = append(out, popup...)
	sep := strings.Repeat("─", cols)
	if f.ScrollOffset > 0 {
		indicator := fmt.Sprintf("──[ ↑ %d lines scrolled | %d new events ↓ (End to follow) ]─", f.ScrollOffset, f.UnreadCount)
		if len(indicator) < cols {
			sep = indicator + strings.Repeat("─", cols-len(indicator))
		}
	}
	out = append(out, sep)
	out = append(out, f.Status)
	composerStartRow := len(out) + 1
	out = append(out, composerLines...)

	cursorRow := composerStartRow + f.CursorRowOffset
	if cursorRow > len(out) {
		cursorRow = len(out)
	}
	return out, cursorRow
}
