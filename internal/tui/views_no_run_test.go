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

// TestViewFooterWithoutRunOffersOnlyWorkingKeys: in the no-run Verify,
// Security and Plan views the footer offers only keys that act, led by
// "s tell the Marshal", and drops the per-view keys that have nothing to
// act on.
func TestViewFooterWithoutRunOffersOnlyWorkingKeys(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, true)
	cases := []struct {
		name   string
		v      boardVisual
		absent string
	}{
		{"verify", boardVisual{theme: th, verView: true, viewNoRun: true}, "return unsupported"},
		{"security", boardVisual{theme: th, secView: true, viewNoRun: true}, "allow for run"},
		{"plan", boardVisual{theme: th, planView: true, viewNoRun: true}, "inspect"},
	}
	for _, c := range cases {
		line, _ := c.v.footerSpans(160)
		got := StripANSI(line)
		if !strings.Contains(got, "tell the Marshal") {
			t.Errorf("%s: footer lacks 'tell the Marshal': %q", c.name, got)
		}
		if strings.Contains(got, c.absent) {
			t.Errorf("%s: footer still offers %q: %q", c.name, c.absent, got)
		}
	}
}
