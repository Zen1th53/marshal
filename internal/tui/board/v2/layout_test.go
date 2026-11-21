package v2

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// An input box not looked at yet is shown as nothing: neither "input
// clear" nor "input waiting", in the chat pane, the Marshal pane and the
// chat strips.
func TestUnknownChatInputShowsNothing(t *testing.T) {
	for _, sc := range screens {
		b := sc.board()
		b.Chat.InputKnown, b.Chat.InputClear = false, false
		b.Marshal.InputKnown, b.Marshal.InputClear = false, false
		var pane []string
		if sc.pane != nil {
			pane = sc.pane(sc.paneW(132), sc.paneHeight(sc.rows))
		}
		for _, l := range RenderChat(b, 132, sc.rows, Theme{Color: ColorNone, Unicode: true}, pane) {
			l = strip(l)
			if strings.Contains(l, "input clear") || strings.Contains(l, "input waiting") || strings.Contains(l, "input     ") && strings.Contains(l, "waiting") {
				t.Errorf("%s: unknown input shown: %q", sc.name, l)
			}
		}
	}
	b := fixturePlanning()
	b.Chat.InputKnown = true
	b.Chat.InputClear = false
	if text := strings.Join(Render(b, 132, 22, Theme{Color: ColorNone, Unicode: true}), "\n"); !strings.Contains(text, "input     waiting") {
		t.Errorf("seen non-empty input not shown as waiting")
	}
}

// On a tall terminal every pane keeps the height its content needs: the
// main list pane may grow a little, and the rows left over stay blank
// below the panes instead of stretching them to the key bar.
func TestTallTerminalLeavesRoomBelowPanes(t *testing.T) {
	for _, sc := range []screenCase{screens[0], screens[2]} {
		for _, cols := range []int{190, 132} { // two columns; narrower stacks them
			lines := sc.render(cols, 52, Theme{Color: ColorNone, Unicode: true})
			last := -1
			for i, l := range lines {
				if strings.ContainsAny(strip(l), "╰") {
					last = i
				}
			}
			if last < 0 || last > 40 {
				t.Errorf("%s %dx52: panes end on row %d, want them to stop where their content does", sc.name, cols, last)
			}
			for i := last + 1; i < 52-2; i++ {
				if strings.TrimSpace(strip(lines[i])) != "" {
					t.Errorf("%s %dx52: row %d below the panes is not blank: %q", sc.name, cols, i, strip(lines[i]))
				}
			}
		}
	}
	// With many steps the steps pane grows to hold them all.
	b := fixturePlanning()
	at := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)
	b.Steps.Items = nil
	for i := 0; i < 30; i++ {
		b.Steps.Items = append(b.Steps.Items, vm.Step{At: at, Kind: "read", Text: "step"})
	}
	text := strip(strings.Join(Render(b, 190, 52, Theme{Color: ColorNone, Unicode: true}), "\n"))
	if n := strings.Count(text, "read     step"); n != 30 {
		t.Errorf("tall steps pane shows %d of 30 steps", n)
	}
}
