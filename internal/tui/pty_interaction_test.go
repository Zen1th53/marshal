//go:build linux

package tui

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Interaction conformance.
//
// These drive the compiled binary over a real pseudo-terminal and assert the
// interaction contract that the previous REPL-style loop violated: a redraw must
// update the screen in place, and Tab must complete without ever submitting.
//
// Assertions run against a replayed screen rather than the raw write log,
// because the raw stream contains every intermediate repaint. What matters is
// what the user is left looking at.

// vtScreen replays a terminal byte stream into a character grid, honouring the
// cursor addressing and erase sequences the workspace emits.
func vtScreen(raw string, rows, cols int) []string {
	grid := make([][]rune, rows)
	for i := range grid {
		grid[i] = []rune(strings.Repeat(" ", cols))
	}
	r, c := 0, 0

	runes := []rune(raw)
	i := 0
	for i < len(runes) {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			j := i + 2
			for j < len(runes) && !((runes[j] >= 'A' && runes[j] <= 'Z') || (runes[j] >= 'a' && runes[j] <= 'z')) {
				j++
			}
			if j >= len(runes) {
				break
			}
			params := string(runes[i+2 : j])
			switch runes[j] {
			case 'H':
				pr, pc := 1, 1
				fmt.Sscanf(params, "%d;%d", &pr, &pc)
				r, c = pr-1, pc-1
			case 'J':
				for k := range grid {
					grid[k] = []rune(strings.Repeat(" ", cols))
				}
				r, c = 0, 0
			case 'K':
				if r >= 0 && r < rows {
					for k := c; k < cols; k++ {
						grid[r][k] = ' '
					}
				}
			case 'G':
				pc := 1
				fmt.Sscanf(params, "%d", &pc)
				c = pc - 1
			}
			i = j + 1
			continue
		}

		switch runes[i] {
		case '\r':
			c = 0
		case '\n':
			r++
			if r >= rows {
				r = rows - 1
			}
		default:
			if r >= 0 && r < rows && c >= 0 && c < cols {
				grid[r][c] = runes[i]
			}
			c++
		}
		i++
	}

	out := make([]string, rows)
	for i := range grid {
		out[i] = strings.TrimRight(string(grid[i]), " ")
	}
	return out
}

func (s *ptySession) screen(rows, cols int) []string {
	return vtScreen(s.output(), rows, cols)
}

func (s *ptySession) screenText(rows, cols int) string {
	return StripANSI(strings.Join(s.screen(rows, cols), "\n"))
}

// composerLine returns the current input row as the user sees it.
func (s *ptySession) composerLine(rows, cols int) string {
	for _, line := range s.screen(rows, cols) {
		plain := StripANSI(line)
		if strings.HasPrefix(strings.TrimSpace(plain), PromptMarker) {
			return strings.TrimSpace(plain)
		}
	}
	return ""
}

func countOnScreen(text, needle string) int {
	return strings.Count(text, needle)
}

// Test A: Tab must complete, never execute.
func TestPTYTabCompletesWithoutExecuting(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	s.send("/mo")
	s.send("\t")
	time.Sleep(400 * time.Millisecond)

	text := s.screenText(rows, cols)

	// The buffer must still hold an unsubmitted command.
	line := s.composerLine(rows, cols)
	if line == "" {
		t.Fatalf("no composer row on screen:\n%s", text)
	}
	if !strings.Contains(line, "/mo") {
		t.Errorf("Tab lost the typed text; composer shows %q", line)
	}

	// Executing /mode would print its confirmation. It must not have run.
	if strings.Contains(text, "Operating mode switched") {
		t.Errorf("Tab executed the command:\n%s", text)
	}

	// Exactly one composer.
	if n := countOnScreen(text, PromptMarker); n != 1 {
		t.Errorf("expected exactly 1 composer, found %d:\n%s", n, text)
	}
}

// Test B: Tab many times must not accumulate chrome.
func TestPTYRepeatedTabKeepsOneComposerAndStatusline(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	s.send("/mo")
	for i := 0; i < 50; i++ {
		s.send("\t")
	}
	time.Sleep(500 * time.Millisecond)

	text := s.screenText(rows, cols)

	if n := countOnScreen(text, PromptMarker); n != 1 {
		t.Errorf("after 50 Tabs expected 1 composer, found %d:\n%s", n, text)
	}
	if n := countOnScreen(text, "MANUAL"); n != 1 {
		t.Errorf("after 50 Tabs expected 1 statusline, found %d:\n%s", n, text)
	}
	// The legacy banner must not reappear in any form.
	if strings.Contains(text, "[MARSHAL]-[") {
		t.Errorf("legacy prompt banner returned:\n%s", text)
	}
}

// Test C: cursor movement edits the middle of the buffer exactly.
func TestPTYCursorEditingProducesExactBuffer(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	s.send("/rote")
	// Move left twice to sit between 'o' and 't', then insert 'u' -> "/route".
	s.send("\x1b[D")
	s.send("\x1b[D")
	s.send("u")
	time.Sleep(300 * time.Millisecond)

	line := s.composerLine(rows, cols)
	if !strings.Contains(line, "/route") {
		t.Errorf("expected buffer /route after mid-line insert, composer shows %q", line)
	}
}

// Test D: history recall after executing commands.
func TestPTYHistoryRecall(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	s.sendLine("/status")
	s.sendLine("/budget")
	time.Sleep(400 * time.Millisecond)

	s.send("\x1b[A") // most recent
	time.Sleep(450 * time.Millisecond)
	if line := s.composerLine(rows, cols); !strings.Contains(line, "/budget") {
		t.Errorf("Up should recall /budget, composer shows %q", line)
	}

	s.send("\x1b[A") // one older
	time.Sleep(450 * time.Millisecond)
	if line := s.composerLine(rows, cols); !strings.Contains(line, "/status") {
		t.Errorf("second Up should recall /status, composer shows %q", line)
	}
}

// Test E: candidate selection with arrows, accepted with Enter, without
// executing the command.
func TestPTYCompletionSelectionDoesNotExecute(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	s.send("/ro")
	s.send("\t")
	time.Sleep(300 * time.Millisecond)

	if !strings.Contains(s.screenText(rows, cols), "/rollback") {
		t.Fatalf("expected /rollback among candidates:\n%s", s.screenText(rows, cols))
	}

	s.send("\x1b[B") // next candidate
	s.send("\r")     // accept
	time.Sleep(400 * time.Millisecond)

	text := s.screenText(rows, cols)
	// Accepting must not run the command: /rollback without an id prints usage.
	if strings.Contains(text, "Usage: /rollback") {
		t.Errorf("Enter on the completion popup executed the command:\n%s", text)
	}
	if n := countOnScreen(text, PromptMarker); n != 1 {
		t.Errorf("expected 1 composer after accepting, found %d", n)
	}
}

// Test F: resizing while the completion popup is open must not corrupt the view.
func TestPTYResizeWithCompletionOpen(t *testing.T) {
	const rows, cols = 30, 120
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	s.send("/ro")
	s.send("\t")
	time.Sleep(300 * time.Millisecond)

	setWinsize(s.master, 24, 80)
	syscall.Kill(s.cmd.Process.Pid, syscall.SIGWINCH)
	time.Sleep(500 * time.Millisecond)

	// Replay at the original grid: the stream contains frames drawn before and
	// after the resize, and the emulator needs one consistent geometry.
	text := s.screenText(rows, cols)
	if n := countOnScreen(text, PromptMarker); n != 1 {
		t.Errorf("after resize expected 1 composer, found %d:\n%s", n, text)
	}
	if n := countOnScreen(text, "MANUAL"); n != 1 {
		t.Errorf("after resize expected 1 statusline, found %d:\n%s", n, text)
	}
	if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
		t.Fatal("session died on resize with the popup open")
	}
}

// Test G/H: a state change while typing must preserve the input buffer.
func TestPTYStateChangePreservesInput(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	// Type a partial command, then force a state refresh via a resize repaint.
	s.send("/inspect partial-input")
	time.Sleep(200 * time.Millisecond)

	setWinsize(s.master, rows, cols)
	syscall.Kill(s.cmd.Process.Pid, syscall.SIGWINCH)
	time.Sleep(500 * time.Millisecond)

	line := s.composerLine(rows, cols)
	if !strings.Contains(line, "partial-input") {
		t.Errorf("repaint destroyed the input buffer; composer shows %q", line)
	}
	if n := countOnScreen(s.screenText(rows, cols), PromptMarker); n != 1 {
		t.Errorf("repaint duplicated the composer, found %d", n)
	}
}

// Test I: many refreshes leave exactly one statusline and one composer.
func TestPTYManyRefreshesKeepSingleChrome(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	for i := 0; i < 40; i++ {
		s.send("x")
		s.send("\x7f") // backspace
	}
	time.Sleep(500 * time.Millisecond)

	text := s.screenText(rows, cols)
	if n := countOnScreen(text, PromptMarker); n != 1 {
		t.Errorf("expected 1 composer after 80 edits, found %d:\n%s", n, text)
	}
	if n := countOnScreen(text, "MANUAL"); n != 1 {
		t.Errorf("expected 1 statusline after 80 edits, found %d:\n%s", n, text)
	}
}

// The statusline must name the project by path, never by the runtime's
// synthetic identifier.
func TestPTYStatuslineShowsRealProject(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")
	time.Sleep(300 * time.Millisecond)

	text := s.screenText(rows, cols)
	if strings.Contains(text, "PROJECT-local") {
		t.Errorf("statusline shows the runtime placeholder:\n%s", text)
	}
}
