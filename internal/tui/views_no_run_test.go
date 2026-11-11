package tui

import (
	"strings"
	"testing"
)

// TestViewsWithoutRunSayNoRunYet: before the first run, the Verify,
// Security and Plan views show only the plain "No run yet" line, with no
// "not readable" error.
func TestViewsWithoutRunSayNoRunYet(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true)}
	cases := []struct {
		name  string
		lines []string
	}{
		{"verify", first(v.verifyLines(verifyPaint{Snap: verifySnapshot{NoRun: true}}, 120))},
		{"security", first(v.securityLines(securityPaint{Snap: securitySnapshot{NoRun: true}}, 120))},
		{"plan", v.planLines(planPaint{Snap: planSnapshot{NoRun: true}}, 120, 30)},
	}
	for _, c := range cases {
		if len(c.lines) != 2 {
			t.Errorf("%s: want 2 lines, got %d: %q", c.name, len(c.lines), c.lines)
			continue
		}
		body := strings.TrimRight(StripANSI(c.lines[1]), " ")
		if body != viewNoRunLine {
			t.Errorf("%s: body = %q, want %q", c.name, body, viewNoRunLine)
		}
		for _, l := range c.lines {
			if strings.Contains(StripANSI(l), "not readable") {
				t.Errorf("%s: no-run view shows a read error: %q", c.name, l)
			}
		}
	}
}

// first returns the lines of a (lines, sel) return pair.
func first(lines []string, _ int) []string { return lines }

// TestViewsLoadFailureIsPlain: a failed first read shows the plain
// viewLoadFailed line in every view, never the raw store error.
func TestViewsLoadFailureIsPlain(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, true)}
	const errText = "open /x/state.db: no such file"
	cases := []struct {
		name  string
		lines []string
	}{
		{"memory", v.memTop(memoryPaint{Snap: memorySnapshot{Err: errText, StorePath: "/mem"}}, 120)},
		{"plan", v.planLines(planPaint{Snap: planSnapshot{Err: errText}}, 120, 30)},
		{"provider", first(v.providerLines(providerPaint{Snap: providerSnapshot{Err: errText}}, 120))},
		{"security", first(v.securityLines(securityPaint{Snap: securitySnapshot{Err: errText}}, 120))},
		{"verify", first(v.verifyLines(verifyPaint{Snap: verifySnapshot{Err: errText}}, 120))},
	}
	for _, c := range cases {
		body := StripANSI(strings.Join(c.lines, "\n"))
		if !strings.Contains(body, viewLoadFailed) {
			t.Errorf("%s: want %q in %q", c.name, viewLoadFailed, body)
		}
		if strings.Contains(body, "state.db") {
			t.Errorf("%s: shows raw error: %q", c.name, body)
		}
	}
}

// TestViewFooterWithoutRunOffersOnlyWorkingKeys: in the no-run Verify,
// Security and Plan views the footer offers only keys that act, led by
// "esc board" and "s say", and drops the per-view keys that have nothing to
// act on.
func TestViewFooterWithoutRunOffersOnlyWorkingKeys(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, true)
	w := emptyBoardWorkspace(t)
	defer w.Close()
	cases := []struct {
		name   string
		v      boardVisual
		open   func(bool)
		absent string
	}{
		{"verify", boardVisual{theme: th, verView: true, viewNoRun: true}, w.verView.Store, "return unsupported"},
		{"security", boardVisual{theme: th, secView: true, viewNoRun: true}, w.secView.Store, "allow for run"},
		{"plan", boardVisual{theme: th, planView: true, viewNoRun: true}, w.planView.Store, "inspect"},
	}
	for _, c := range cases {
		c.open(true)
		c.v.hints = w.boardHints(c.v)
		c.open(false)
		line, _ := c.v.footerSpans(160)
		got := StripANSI(line)
		if !strings.HasPrefix(got, " esc board  s say") {
			t.Errorf("%s: footer does not lead with esc board, s say: %q", c.name, got)
		}
		if strings.Contains(got, c.absent) {
			t.Errorf("%s: footer still offers %q: %q", c.name, c.absent, got)
		}
	}
}
