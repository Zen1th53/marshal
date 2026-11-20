package v2

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The NOW strip's marshal column keeps one blank cell before "│": a line
// that fills the column is cut with "…" rather than run up to the rule.
func TestNowStripKeepsGapBeforeRule(t *testing.T) {
	for _, cols := range []int{132, 100, 80} {
		for _, text := range []string{
			"claude Opus 5.5 · planning the tasks · 24s",
			"claude Opus 5.5 · planning the tasks · 1m with more words after",
			"short",
		} {
			c := newCanvas(cols, 2, true)
			nowStrip(c, 1, vm.NowLine{Marshal: text, Work: "none yet", FromYou: "nothing", FromYouOK: true})
			row := strip(c.lines(Theme{Color: ColorNone, Unicode: true})[1])
			_, after, ok := strings.Cut(row, " marshal ")
			if !ok {
				t.Fatalf("%d cols: no marshal label in %q", cols, row)
			}
			col, _, ok := strings.Cut(after, "│")
			if !ok {
				t.Fatalf("%d cols: no rule in %q", cols, row)
			}
			if !strings.HasSuffix(col, " ") {
				t.Fatalf("%d cols: no gap before the rule: %q", cols, row)
			}
			if long := len([]rune(text)) >= len([]rune(col)); long && !strings.Contains(col, "…") {
				t.Fatalf("%d cols: long text not cut with an ellipsis: %q", cols, col)
			}
		}
	}
}
