package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

func rowsVisual(b model.Board, mode ThemeMode, utf8 bool) boardVisual {
	now := time.Date(2026, 11, 2, 14, 2, 31, 0, time.UTC)
	return boardVisual{board: b, now: now, theme: NewTheme(mode, utf8, true), frame: anim.Frame{}}
}

func taskLine(t *testing.T, lines []string, id string) string {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(StripANSI(l), id) {
			return l
		}
	}
	t.Fatalf("no row for %s in:\n%s", id, StripANSI(strings.Join(lines, "\n")))
	return ""
}

// A recovered run shows tasks working or in review with no lane: their rows
// say stalled, still, in the waiting colour, and never carry a spinner glyph.
func TestStalledTaskRowIsStill(t *testing.T) {
	b := model.Board{
		Tasks: []model.TaskRow{
			{ID: "T-1", State: model.TaskWorking, Worker: "codex"},
			{ID: "T-2", State: model.TaskReview, Worker: "codex"},
			{ID: "T-3", State: model.TaskWorking, Worker: "claude"},
		},
		Lanes: []model.Lane{{Worker: "claude-1", Provider: "claude", TaskID: "T-3", State: model.LaneWorking}},
	}
	for _, utf8 := range []bool{true, false} {
		v := rowsVisual(b, ThemeDefault, utf8)
		lines := v.tasks(layout.Pane{ID: layout.Tasks, Rect: layout.Rect{W: 120, H: 8}})
		glyph := "◌"
		if !utf8 {
			glyph = "!"
		}
		for _, id := range []string{"T-1", "T-2"} {
			line := taskLine(t, lines, id)
			plain := StripANSI(line)
			if !strings.Contains(plain, glyph+" stalled") {
				t.Errorf("utf8 %v: %s row %q is not stalled", utf8, id, plain)
			}
			if !strings.Contains(line, v.fg("waiting", glyph+" stalled")) {
				t.Errorf("utf8 %v: %s stalled not in the waiting colour", utf8, id)
			}
			for _, g := range append(anim.Braille[:], "*") {
				if strings.Contains(plain, g+" ") {
					t.Errorf("utf8 %v: stalled %s row carries spinner %q: %q", utf8, id, g, plain)
				}
			}
		}
		if plain := StripANSI(taskLine(t, lines, "T-3")); !strings.Contains(plain, "working") || strings.Contains(plain, "stalled") {
			t.Errorf("utf8 %v: a task with a live lane is not working: %q", utf8, plain)
		}
	}
}

// The ID column fits the longest id up to its cap, and a long scope keeps
// its end, where the file name is.
func TestTaskIDWholeAndScopeKeepsFileName(t *testing.T) {
	b := model.Board{Tasks: []model.TaskRow{
		{ID: "T-0123a", State: model.TaskQueued, Scope: []string{"internal/tui/board/widgets/a/very/deep/path/that/never/ends/in/this/tree/memory_recall.go"}},
		{ID: "T-1", State: model.TaskQueued, Scope: []string{"a.go"}},
		{ID: "T-ABCDEFGHIJKLMNOP", State: model.TaskQueued},
	}}
	// 73 is the widest compact pane (compact is under 74); its scope column
	// still holds the cut file name beside the 12-cell "sent back" column.
	for _, w := range []int{73, 120} {
		v := rowsVisual(b, ThemeNoColor, true)
		lines := v.tasks(layout.Pane{ID: layout.Tasks, Rect: layout.Rect{W: w, H: 8}})
		for _, l := range lines {
			if got := VisibleLen(l); got != w {
				t.Errorf("width %d: line is %d cells: %q", w, got, l)
			}
		}
		long := taskLine(t, lines, "T-0123a")
		if !strings.Contains(long, "T-0123a ") {
			t.Errorf("width %d: id cut: %q", w, long)
		}
		if !strings.Contains(long, "…") || !strings.Contains(long, "memory_recall.go") || strings.Contains(long, "internal/tui") {
			t.Errorf("width %d: scope not cut from the left: %q", w, long)
		}
		capped := taskLine(t, lines, "T-ABCDEFGHIJ")
		if !strings.Contains(capped, "T-ABCDEFGHIJ… ") || strings.Contains(capped, "KLMNOP") {
			t.Errorf("width %d: id over the cap not cut at 12: %q", w, capped)
		}
		// Every row's state starts in the same column as the header's.
		head := lines[1]
		at := func(s, sub string) int { return VisibleLen(s[:strings.Index(s, sub)]) }
		col := at(head, "state")
		for _, id := range []string{"T-0123a", "T-1 ", "T-ABCDEFGHIJ"} {
			if got := at(taskLine(t, lines, id), "· queued"); got != col {
				t.Errorf("width %d: %s state at %d, header at %d", w, id, got, col)
			}
		}
	}
}

func TestTruncateLeft(t *testing.T) {
	for _, c := range []struct {
		in   string
		w    int
		want string
	}{
		{"abc", 5, "abc"}, {"abcdef", 4, "…def"}, {"abcdef", 1, "…"}, {"abcdef", 0, ""}, {"a/日本.go", 6, "…本.go"},
	} {
		if got := truncateLeft(c.in, c.w); got != c.want {
			t.Errorf("truncateLeft(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
}

// The state-change pulse reaches the painter: the changed row is bold for
// the whole of its width, the others are not.
func TestPulsingTaskRowIsBold(t *testing.T) {
	repaint := make(chan struct{}, 1)
	a := anim.New(repaint, anim.RealClock{})
	defer a.Close()
	before := model.Board{Tasks: []model.TaskRow{{ID: "T-1", State: model.TaskAccepted}, {ID: "T-2", State: model.TaskQueued}}}
	after := model.Board{Tasks: []model.TaskRow{{ID: "T-1", State: model.TaskMerged}, {ID: "T-2", State: model.TaskQueued}}}
	a.Update(before)
	a.Update(after)
	v := rowsVisual(after, ThemeDefault, true)
	v.frame = a.Frame()
	if !v.frame.Pulsing("T-1") {
		t.Fatal("no pulse for the changed task")
	}
	lines := v.tasks(layout.Pane{ID: layout.Tasks, Rect: layout.Rect{W: 120, H: 6}})
	pulsed, plain := taskLine(t, lines, "T-1"), taskLine(t, lines, "T-2")
	if !strings.HasPrefix(pulsed, "\x1b[1m") || strings.Contains(plain, "\x1b[1m") {
		t.Fatalf("pulse not painted: T-1 %q, T-2 %q", pulsed, plain)
	}
	// Bold survives every colour reset inside the row.
	if strings.Count(pulsed, "\x1b[0m") != strings.Count(pulsed, "\x1b[0m\x1b[1m")+1 {
		t.Fatalf("a reset inside the pulsed row drops the bold: %q", pulsed)
	}
	if VisibleLen(pulsed) != 120 {
		t.Fatalf("pulsed row is %d cells", VisibleLen(pulsed))
	}
	v.theme = NewTheme(ThemeNoColor, true, true)
	if l := taskLine(t, v.tasks(layout.Pane{ID: layout.Tasks, Rect: layout.Rect{W: 120, H: 6}}), "T-1"); strings.Contains(l, "\x1b") {
		t.Fatalf("no-colour pulse wrote an escape: %q", l)
	}
}

// The lanes summary says how many lanes are running, in plain words, never
// the lane cap.
func TestLanesSummaryRunning(t *testing.T) {
	r := marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "T-1", Worker: "codex", State: marshal.Dispatched}}}
	b := model.Project(model.Snapshot{RunID: "R1", Run: r, Workers: []model.Worker{{ID: "codex-1", Provider: "codex", TaskID: "T-1"}}}, nil)
	v := rowsVisual(b, ThemeNoColor, true)
	title := StripANSI(v.lanes(layout.Pane{ID: layout.Lanes, Rect: layout.Rect{W: 80, H: 4}})[0])
	if !strings.Contains(title, "1 running") || strings.Contains(title, "of ") || strings.Contains(title, "live") {
		t.Fatalf("lanes summary %q, want 1 running", title)
	}
	// With no busy lane the summary reads idle.
	bi := model.Project(model.Snapshot{RunID: "R1", Run: marshal.Run{State: marshal.Dispatching}}, nil)
	vi := rowsVisual(bi, ThemeNoColor, true)
	if title := StripANSI(vi.lanes(layout.Pane{ID: layout.Lanes, Rect: layout.Rect{W: 80, H: 4}})[0]); !strings.Contains(title, "idle") {
		t.Fatalf("empty lanes summary %q, want idle", title)
	}
}

// A task's files read as a list: "calc.py, test_calc.py", a space after
// each comma.
func TestTaskScopeListsFilesWithSpaces(t *testing.T) {
	b := model.Board{Tasks: []model.TaskRow{
		{ID: "divide", Worker: "codex", State: model.TaskQueued, Scope: []string{"calc.py", "test_calc.py"}},
	}}
	for _, w := range []int{73, 120} {
		v := rowsVisual(b, ThemeNoColor, true)
		lines := v.tasks(layout.Pane{ID: layout.Tasks, Rect: layout.Rect{W: w, H: 8}})
		if row := taskLine(t, lines, "divide"); !strings.Contains(row, "calc.py, test_calc.py") {
			t.Errorf("width %d: the files are not listed with a space: %q", w, row)
		}
	}
}
