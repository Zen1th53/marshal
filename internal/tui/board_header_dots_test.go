package tui

import (
	"strings"
	"testing"
	"time"
)

func TestHeaderProviderDots(t *testing.T) {
	now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
	for _, mode := range []ThemeMode{ThemeNoColor, ThemeDefault} {
		for _, withHost := range []bool{false, true} {
			v := boardVisual{
				board: demoBoard(now), now: now, theme: NewTheme(mode, true, true), edition: "Marshal",
				health: map[string]string{"codex": "●", "claude": "◐", "agy": "×", "opencode": "○"},
				arch:   true, archLoaded: true, archRev: 7,
			}
			if withHost {
				host := demoHost(t)
				v.host = &host
			}
			for _, width := range []int{120, 200} {
				header := v.header(width)
				plain := StripANSI(header)
				if got := VisibleLen(header); got != width {
					t.Errorf("mode %v host %v width %d: got %d cells", mode, withHost, width, got)
				}
				for _, dot := range []string{"●", "◐", "×", "○"} {
					if !strings.Contains(plain, dot) {
						t.Errorf("width %d: missing provider status %q in %q", width, dot, plain)
					}
				}
				if width == 120 && !strings.Contains(plain, "● ◐ × ○") {
					t.Errorf("compact provider statuses out of order in %q", plain)
				}
				if mode == ThemeDefault {
					for color, dot := range map[string]string{"merged": "●", "waiting": "◐", "failed": "×", "dim": "○"} {
						if !strings.Contains(header, v.fg(color, dot)) {
							t.Errorf("width %d: missing %s status color for %q", width, color, dot)
						}
					}
				}
				if !strings.Contains(plain, StripANSI(v.archChip(false))) && !strings.Contains(plain, StripANSI(v.archChip(true))) {
					t.Errorf("width %d: missing architecture chip in %q", width, plain)
				}
				if width == 200 {
					for _, provider := range []string{"codex ●", "claude ◐", "agy ×", "opencode ○"} {
						if !strings.Contains(plain, provider) {
							t.Errorf("wide header missing %q in %q", provider, plain)
						}
					}
				}
			}
		}
	}
}
