//go:build linux

package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// footerIDs paints the workspace at cols x rows and returns the hint bar's
// buttons, left to right, and the hint bar's text. The classic Board draws
// the hint bar on the last row; Board v2 draws it as its key bar, above the
// status row. Either way it is the lowest row that holds action buttons.
func footerIDs(t *testing.T, w *Workspace, cols, rows int) ([]string, string) {
	t.Helper()
	lines, _, _ := w.frameLines(cols, rows)
	w.boardHits.mu.Lock()
	hits := slices.Clone(w.boardHits.hits)
	w.boardHits.mu.Unlock()
	slices.SortFunc(hits, func(a, b boardHit) int { return a.x0 - b.x0 })
	row := -1
	for _, h := range hits {
		if h.kind == hitAction && h.row > row && h.row >= rows-2 {
			row = h.row
		}
	}
	var ids []string
	for _, h := range hits {
		if h.row == row && h.kind == hitAction {
			ids = append(ids, h.id)
		}
	}
	if row < 0 {
		row = rows - 1
	}
	return ids, StripANSI(lines[row])
}

// v2CardButtons paints the workspace and returns the keys of the Board v2
// card buttons on screen, left to right, with the frame's text.
func v2CardButtons(t *testing.T, w *Workspace, cols, rows int) ([]string, string) {
	t.Helper()
	lines, _, _ := w.frameLines(cols, rows)
	w.boardHits.mu.Lock()
	hits := slices.Clone(w.boardHits.hits)
	w.boardHits.mu.Unlock()
	slices.SortFunc(hits, func(a, b boardHit) int { return a.x0 - b.x0 })
	var keys []string
	for _, h := range hits {
		switch {
		case h.row >= rows-2:
		case h.kind == hitV2Key:
			keys = append(keys, h.id)
		case h.kind == hitGuard:
			// An owner card's chip is the classic card's own region.
			keys = append(keys, string(h.action))
		}
	}
	return keys, StripANSI(strings.Join(lines, "\n"))
}

// footerCell is the cell of label on the hint bar, 1-based for click.
func footerCell(t *testing.T, w *Workspace, cols, rows int, label string) (int, int) {
	t.Helper()
	lines, _, _ := w.frameLines(cols, rows)
	for row := rows - 1; row >= max(0, rows-2); row-- {
		plain := StripANSI(lines[row])
		if i := strings.Index(plain, label); i >= 0 {
			return row + 1, VisibleLen(plain[:i]) + 1
		}
	}
	t.Fatalf("hint bar lacks %q: %q", label, StripANSI(lines[rows-1]))
	return 0, 0
}

// runHintWorkspace is boardWorkspace with the inbox emptied and the Tasks
// pane on task, so a test sets one state at a time.
func runHintWorkspace(t *testing.T, task string) *Workspace {
	t.Helper()
	w, _ := boardWorkspace(t)
	w.permissions.inbox = nil
	w.boardUI.focus, w.boardUI.task = layout.Tasks, task
	return w
}

// TestFitHintsRule: fitHints drops the tail's optional items first, then
// shortens labels, then the lead's later items; "space actions" and "? help"
// go only once nothing less important is left.
func TestFitHintsRule(t *testing.T) {
	items := []hintItem{
		{"say.build", "s", "say what to build"}, {"firstuse.chat", "⏎", "chat"}, {"marshal.say", "x", "say it"},
		{"palette", "space", "actions"}, {"marshal.pick", "m", "Marshal"}, {"marshal.chat", "F11", "chat"}, {"help", "?", "help"},
	}
	ids := func(list []hintItem) string {
		var out []string
		for _, it := range list {
			out = append(out, it.id+"="+it.hint)
		}
		return strings.Join(out, " ")
	}
	width := func(list []hintItem) int {
		n := 0
		for _, it := range list {
			n += VisibleLen(it.key) + 1 + VisibleLen(it.hint) + 2
		}
		return n
	}
	full := width(items)
	for _, c := range []struct {
		w    int
		want string
	}{
		{full, "say.build=say what to build firstuse.chat=chat marshal.say=say it palette=actions marshal.pick=Marshal marshal.chat=chat help=help"},
		{full - 1, "say.build=say what to build firstuse.chat=chat marshal.say=say it palette=actions marshal.pick=Marshal help=help"},
		{62, "say.build=say what to build firstuse.chat=chat marshal.say=say it palette=actions help=help"},
		{61, "say.build=say firstuse.chat=chat marshal.say=say palette=actions help=help"},
		{44, "say.build=say firstuse.chat=chat palette=actions help=help"},
		{37, "say.build=say palette=actions help=help"},
		{29, "say.build=say help=help"},
		{14, "help=help"},
	} {
		if got := ids(fitHints(items, 0, c.w)); got != c.want {
			t.Errorf("width %d (full %d):\n got %s\nwant %s", c.w, full, got, c.want)
		}
	}
}

// The hint bar's own rows are registry rows: unique ids beside the keys',
// a label each, a Why wherever they gate themselves, and listed only, so
// neither help nor the palette shows them.
func TestBoardHintRows(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range boardKeymap {
		seen[k.ID] = true
	}
	actions := boardPaletteActions(nil)
	for _, k := range boardHintKeymap {
		if seen[k.ID] {
			t.Errorf("duplicate ID %q", k.ID)
		}
		seen[k.ID] = true
		if k.Hint == "" || !k.Listed || (k.When != nil && k.Why == nil) {
			t.Errorf("hint row %+v", k)
		}
		if got, ok := boardAction(k.ID); !ok || got.ID != k.ID {
			t.Errorf("boardAction(%q) did not find the row", k.ID)
		}
		if slices.ContainsFunc(actions, func(a PaletteAction) bool { return a.ID == k.ID }) {
			t.Errorf("the palette lists hint row %q", k.ID)
		}
	}
}

// goldenHints is the hint bar a golden's bare visual paints: boardHints with
// nothing armed or typed and every action available — the open view's keys,
// the first-use card's or the Board's own lead, then the tail.
// TestBoardHintsFollowState checks the live list against a workspace.
func goldenHints(v boardVisual) []string {
	lead := v.viewHints()
	switch {
	case lead != nil:
	case waitingGates(v.gates) > 0:
		lead = []string{"gate.reply"}
	case v.empty && (v.planning || v.goalGiven):
		lead = []string{"marshal.say", "firstuse.chat", "marshal.pick"}
	case v.empty:
		lead = []string{"say.build", "firstuse.chat", "firstuse.verify", "marshal.pick"}
	default:
		lead = []string{"board.move", "task.inspect", "marshal.say"}
	}
	out := append(lead, "palette")
	if !slices.Contains(lead, "marshal.pick") {
		out = append(out, "marshal.pick")
	}
	if v.docked {
		out = append(out, "marshal.chat")
	}
	return append(out, "help")
}
