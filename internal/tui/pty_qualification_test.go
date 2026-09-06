//go:build linux

package tui

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"
)

// separatorWidth reports the width of the frame's header rule, which spans the
// full terminal. It is the cheapest observable proof that a repaint used the
// current geometry.
func separatorWidth(s *ptySession, rows, cols int) int {
	for _, line := range s.screen(rows, cols) {
		plain := StripANSI(line)
		if strings.Count(plain, "─") > 10 {
			return VisibleLen(strings.TrimRight(plain, " "))
		}
	}
	return 0
}

// TestPTYEscDismissesOverlayWithoutDestroyingDraft is the regression for the
// audit finding that Esc cleared a typed draft when closing a completion popup.
func TestPTYEscDismissesOverlayWithoutDestroyingDraft(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	const draft = "/inspect my-precious-draft"
	s.send(draft)
	time.Sleep(300 * time.Millisecond)
	if line := s.composerLine(rows, cols); !strings.Contains(line, "my-precious-draft") {
		t.Fatalf("draft did not reach the composer: %q", line)
	}

	// Tab opens the completion popup over the draft.
	s.send("\t")
	time.Sleep(350 * time.Millisecond)

	// Esc dismisses the popup only.
	s.send("\x1b")
	time.Sleep(400 * time.Millisecond)

	line := s.composerLine(rows, cols)
	if !strings.Contains(line, "my-precious-draft") {
		t.Errorf("Esc destroyed the draft; composer shows %q", line)
	}

	text := s.screenText(rows, cols)
	if strings.Contains(text, "Tab next · ↑↓ select") {
		t.Error("Esc did not dismiss the completion popup")
	}
	if n := strings.Count(text, PromptMarker); n != 1 {
		t.Errorf("expected exactly 1 composer after Esc, found %d", n)
	}
}

// TestPTYEscOnPaletteKeepsDraft covers the same contract for the command palette.
func TestPTYEscOnPaletteKeepsDraft(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	s.send("/inspect palette-draft")
	time.Sleep(300 * time.Millisecond)

	s.send("\x10") // Ctrl+P
	time.Sleep(400 * time.Millisecond)
	s.send("\x1b") // Esc
	time.Sleep(400 * time.Millisecond)

	if line := s.composerLine(rows, cols); !strings.Contains(line, "palette-draft") {
		t.Errorf("closing the palette destroyed the draft; composer shows %q", line)
	}
}

// TestPTYEscWithNoOverlayKeepsDraft proves a bare Esc is inert on the composer:
// discarding a line is Ctrl+U, which stays explicit.
func TestPTYEscWithNoOverlayKeepsDraft(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	s.send("/inspect bare-esc-draft")
	time.Sleep(300 * time.Millisecond)
	s.send("\x1b")
	time.Sleep(400 * time.Millisecond)

	if line := s.composerLine(rows, cols); !strings.Contains(line, "bare-esc-draft") {
		t.Errorf("a bare Esc cleared the draft; composer shows %q", line)
	}

	// Ctrl+U still clears deliberately.
	s.send("\x15")
	time.Sleep(300 * time.Millisecond)
	if line := s.composerLine(rows, cols); strings.Contains(line, "bare-esc-draft") {
		t.Errorf("Ctrl+U failed to clear the draft; composer shows %q", line)
	}
}

// TestPTYGrowResizeRepaintsWithoutKeypress is the regression for the audit
// finding that growing the terminal left the old, narrower frame on screen until
// the operator happened to press a key.
func TestPTYGrowResizeRepaintsWithoutKeypress(t *testing.T) {
	const startRows, startCols = 24, 80
	s := startTUI(t, startRows, startCols)
	s.mustSee("MARSHAL")
	time.Sleep(400 * time.Millisecond)

	if w := separatorWidth(s, startRows, startCols); w != startCols {
		t.Fatalf("initial separator width %d, want %d", w, startCols)
	}

	// Grow, and deliberately send NO key.
	const grownRows, grownCols = 30, 120
	setWinsize(s.master, grownRows, grownCols)
	syscall.Kill(s.cmd.Process.Pid, syscall.SIGWINCH)

	deadline := time.Now().Add(5 * time.Second)
	got := 0
	for time.Now().Before(deadline) {
		if got = separatorWidth(s, grownRows, grownCols); got == grownCols {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got != grownCols {
		t.Errorf("grow resize did not repaint without a keypress: separator width %d, want %d",
			got, grownCols)
	}

	// Shrink back, again with no key.
	setWinsize(s.master, startRows, startCols)
	syscall.Kill(s.cmd.Process.Pid, syscall.SIGWINCH)

	deadline = time.Now().Add(5 * time.Second)
	got = 0
	for time.Now().Before(deadline) {
		if got = separatorWidth(s, startRows, startCols); got == startCols {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got != startCols {
		t.Errorf("shrink resize did not repaint without a keypress: separator width %d, want %d",
			got, startCols)
	}
}

// TestPTYTabCyclesThroughCandidates proves repeated Tab advances the selection
// rather than stalling on the first candidate.
func TestPTYTabCyclesThroughCandidates(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	// "/c" matches several commands: /claims, /checkpoint, /cancel, /context...
	s.send("/c")
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		s.send("\t")
		time.Sleep(280 * time.Millisecond)
		line := strings.TrimSpace(strings.TrimPrefix(s.composerLine(rows, cols), PromptMarker))
		if line != "" {
			seen[line] = true
		}
	}

	if len(seen) < 2 {
		var got []string
		for k := range seen {
			got = append(got, k)
		}
		t.Errorf("Tab did not cycle: only reached %v", got)
	}
	fmt.Printf("TAB CYCLE reached %d distinct candidates\n", len(seen))

	if n := strings.Count(s.screenText(rows, cols), PromptMarker); n != 1 {
		t.Errorf("cycling duplicated the composer, found %d", n)
	}
}

// TestPTYBracketedPasteStillIntact guards the behaviour the audit found working,
// so the other fixes cannot regress it.
func TestPTYBracketedPasteStillIntact(t *testing.T) {
	const rows, cols = 24, 100
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	s.send("\x1b[200~line one\nline two\nline three\x1b[201~")
	time.Sleep(700 * time.Millisecond)

	text := s.screenText(rows, cols)
	for _, want := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(text, want) {
			t.Errorf("pasted %q missing from the composer", want)
		}
	}
	for _, banned := range []string{"200~", "201~"} {
		if strings.Contains(text, banned) {
			t.Errorf("raw paste marker %q leaked to the screen", banned)
		}
	}
	if strings.Contains(text, "Unknown command") {
		t.Error("a pasted line executed as a command")
	}

	// Hostile paste must not execute either.
	s.send("\x15")
	s.send("\x1b[200~/quit\n@codex\tmid\no‘zbekcha 中文\x1b[201~")
	time.Sleep(700 * time.Millisecond)
	if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
		t.Fatal("a pasted /quit terminated the session")
	}
}

// TestPTYGraphemeEditingOnRealTerminal proves the grapheme fix survives the full
// path through the terminal, not just the in-process composer.
func TestPTYGraphemeEditingOnRealTerminal(t *testing.T) {
	const rows, cols = 24, 110
	s := startTUI(t, rows, cols)
	s.mustSee("MARSHAL")

	for _, sample := range []struct{ name, text string }{
		{"familyZWJ", "👨‍👩‍👧‍👦"},
		{"flag", "🇺🇿"},
		{"skintone", "👍🏽"},
		{"combining", "é"},
	} {
		s.send("\x15")
		s.send("A" + sample.text)
		time.Sleep(300 * time.Millisecond)

		s.send("\x7f") // one Backspace must remove the whole cluster
		time.Sleep(320 * time.Millisecond)

		line := strings.TrimSpace(strings.TrimPrefix(s.composerLine(rows, cols), PromptMarker))
		if line != "A" {
			t.Errorf("%s: one Backspace left %q, want %q", sample.name, line, "A")
		}
	}
}
