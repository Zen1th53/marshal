package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// TestLaunchScreensCarryVitals: the launch picker, the opening and the Blocked
// screen keep the vitals row on top, as every other screen does, at the plan's
// two widths; with the vitals on the tmux status line they paint none and the
// header moves to the top row.
func TestLaunchScreensCarryVitals(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, false)
	w := &Workspace{homeBoard: true}
	w.launch.blockers = []string{blockedPrefix + " this folder is not a Git repository with a first commit. Run git init, then press r."}
	for _, size := range [][2]int{{160, 40}, {100, 30}} {
		cols, rows := size[0], size[1]
		var hits []boardHit
		v := vitalsFixture(th)
		v.hits = &hits
		v.empty = true
		v.project = "proj"
		v.pick = pickerView{cands: []pickerCand{{provider: "codex", left: 29, known: true}, {provider: "claude", known: false}}, sel: 0}
		for _, outside := range []bool{false, true} {
			l := layout.ComputeFor(cols, rows, layout.Home, v.layoutCounts())
			if outside {
				l = layout.ComputeFor(cols, rows+1, layout.Home, v.layoutCounts()).WithoutVitals()
			}
			vitals, _ := v.vitalsLine(cols)
			for name, frame := range map[string][]string{
				"picker":  w.paintPicker(l, th, v),
				"opening": w.paintOpening(l, th, v, 8, false),
				"blocked": w.paintBlocked(l, th, v),
			} {
				top := StripANSI(frame[0])
				if !outside {
					if top != StripANSI(vitals) {
						t.Errorf("%s %dx%d: top row\n got %q\nwant %q", name, cols, rows, top, StripANSI(vitals))
					}
					checkGolden(t, fmt.Sprintf("launch-%s-%dx%d", name, cols, rows), frame)
				} else if strings.Contains(top, "running") || !strings.Contains(top, "Marshal") {
					t.Errorf("%s %dx%d with the status line: top row %q", name, cols, rows, top)
				}
			}
		}
	}
}
