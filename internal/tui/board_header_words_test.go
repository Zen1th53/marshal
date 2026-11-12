package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// The header state chip is the run's state in the owner's words, first match
// wins over the table in the spec.
func TestHeaderStateWords(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	for _, c := range []struct {
		name string
		v    boardVisual
		want string
	}{
		{"waiting-stop", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.AwaitingUser}}}, "waiting for you"},
		{"paused", boardVisual{theme: th, note: "stopped: worker exited 2", stopped: true, board: model.Board{Run: model.RunHead{State: marshal.Dispatching}}}, "paused"},
		{"owner-waits", boardVisual{theme: th, guard: &guardAlert{}, board: model.Board{Run: model.RunHead{State: marshal.Dispatching}}}, "waiting for you"},
		{"done", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Closed}}}, "done"},
		{"planning", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Drafting}}}, "planning"},
		{"starting", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: ""}}}, "starting"},
		{"running", boardVisual{theme: th, board: model.Board{Run: model.RunHead{State: marshal.Dispatching}}}, "running"},
	} {
		if got := StripANSI(c.v.stateChip()); got != c.want {
			t.Errorf("%s: stateChip %q, want %q", c.name, got, c.want)
		}
	}
}

// The header names the run by its title, never by its id.
func TestHeaderHidesRunID(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, true)
	v := boardVisual{theme: th, now: time.Now(), edition: "Marshal", board: model.Board{Run: model.RunHead{
		ID: "RUN-0123456789abcdef0123456789abcdef", Title: "Add login", State: marshal.Dispatching,
	}}}
	for _, w := range []int{80, 100, 160, 200} {
		got := StripANSI(v.header(w))
		if !strings.Contains(got, "Add login") {
			t.Errorf("header %d lacks the title: %q", w, got)
		}
		if strings.Contains(got, "RUN-") || strings.Contains(got, "R-") {
			t.Errorf("header %d shows the run id: %q", w, got)
		}
	}
}

// The last task column is empty with no returns, else "sent back N×" ("Nx"
// in ASCII), coloured while the task is in rework.
func TestTaskRowSentBack(t *testing.T) {
	row := func(returns int, state model.TaskState) model.Board {
		return model.Board{Tasks: []model.TaskRow{{
			ID: "T-1", Worker: "codex-1", Title: "scope", Scope: []string{"a.go"},
			State: state, Returns: returns, ReworkLimit: 2,
		}}}
	}
	// taskRow is the rendered row for T-1, not the column header.
	taskRow := func(v boardVisual) string {
		for _, l := range v.tasks(layout.Pane{ID: layout.Tasks, Rect: layout.Rect{W: 120, H: 6}}) {
			if plain := StripANSI(l); strings.Contains(plain, "T-1") {
				return plain
			}
		}
		return ""
	}
	if got := taskRow(rowsVisual(row(0, model.TaskWorking), ThemeNoColor, true)); strings.Contains(got, "sent back") {
		t.Errorf("no returns still shows a sent-back count: %q", got)
	}
	if got := taskRow(rowsVisual(row(2, model.TaskRework), ThemeNoColor, true)); !strings.Contains(got, "sent back 2×") {
		t.Errorf("two returns: %q", got)
	}
	if got := taskRow(rowsVisual(row(2, model.TaskRework), ThemeDefault, false)); !strings.Contains(got, "sent back 2x") {
		t.Errorf("two returns in ASCII: %q", got)
	}
}

// The sent back column is there only once a task was sent back, and the
// TASKS title counts one task as "1 task".
func TestTasksSentBackColumnOnlyWhenSentBack(t *testing.T) {
	board := func(returns ...int) model.Board {
		var b model.Board
		for i, r := range returns {
			b.Tasks = append(b.Tasks, model.TaskRow{ID: fmt.Sprintf("T-%d", i+1), State: model.TaskWorking, Scope: []string{"a.go"}, Returns: r})
		}
		b.Run.Total = len(returns)
		return b
	}
	for _, w := range []int{73, 120} {
		lines := rowsVisual(board(0), ThemeNoColor, true).tasks(layout.Pane{ID: layout.Tasks, Rect: layout.Rect{W: w, H: 6}})
		if title := StripANSI(lines[0]); !strings.Contains(title, "TASKS 1 task ·") {
			t.Errorf("width %d: one task's title: %q", w, title)
		}
		if head := StripANSI(lines[1]); strings.Contains(head, "sent back") {
			t.Errorf("width %d: no task was sent back, yet the column shows: %q", w, head)
		}
		lines = rowsVisual(board(0, 1), ThemeNoColor, true).tasks(layout.Pane{ID: layout.Tasks, Rect: layout.Rect{W: w, H: 6}})
		if title := StripANSI(lines[0]); !strings.Contains(title, "TASKS 2 tasks ·") {
			t.Errorf("width %d: two tasks' title: %q", w, title)
		}
		if head := StripANSI(lines[1]); !strings.Contains(head, "sent back") {
			t.Errorf("width %d: a task was sent back, yet the column is gone: %q", w, head)
		}
		for _, l := range lines {
			if got := VisibleLen(l); got != w {
				t.Errorf("width %d: line is %d cells: %q", w, got, l)
			}
		}
	}
}

// The LANES title reads "idle" with no busy lane, else "N running".
func TestLanesTitleIdleAndRunning(t *testing.T) {
	title := func(b model.Board) string {
		v := rowsVisual(b, ThemeNoColor, true)
		return StripANSI(v.lanes(layout.Pane{ID: layout.Lanes, Rect: layout.Rect{W: 80, H: 1}})[0])
	}
	idle := model.Board{Run: model.RunHead{State: marshal.Dispatching}, Lanes: []model.Lane{
		{Worker: "codex-1", Provider: "codex", State: model.LaneIdle},
	}}
	if got := title(idle); !strings.Contains(got, "LANES idle") {
		t.Fatalf("idle lanes title %q", got)
	}
	busy := model.Board{Run: model.RunHead{State: marshal.Dispatching}, Lanes: []model.Lane{
		{Worker: "codex-1", Provider: "codex", State: model.LaneWorking},
		{Worker: "claude-1", Provider: "claude", State: model.LaneWorking},
	}}
	if got := title(busy); !strings.Contains(got, "LANES 2 running") {
		t.Fatalf("busy lanes title %q", got)
	}
}

// A task in review is running: its row spins "review", and the TASKS header,
// the LANES title and the vitals row all count it, though no worker lane is
// live while the Marshal reviews. Before, TASKS said "1 running" while LANES
// said "idle" and the vitals "0 running".
func TestReviewCountsAsRunningEverywhere(t *testing.T) {
	th := NewTheme(ThemeNoColor, true, true)
	b := model.Board{Run: model.RunHead{State: marshal.Dispatching, Total: 1, Running: 1}, Tasks: []model.TaskRow{{
		ID: "multiply", Worker: "codex", Scope: []string{"calc.py"}, State: model.TaskReview,
	}}}
	v := boardVisual{theme: th, now: time.Now(), edition: "Marshal", board: b}
	tasks := StripANSI(v.tasks(layout.Pane{ID: layout.Tasks, Rect: layout.Rect{W: 120, H: 6}})[0])
	lanes := StripANSI(v.lanes(layout.Pane{ID: layout.Lanes, Rect: layout.Rect{W: 120, H: 4}})[0])
	vitals, _ := v.vitalsLine(160)
	vitals = StripANSI(vitals)
	for name, got := range map[string]string{"TASKS": tasks, "LANES": lanes, "vitals": vitals} {
		if !strings.Contains(got, "1 running") {
			t.Errorf("%s does not count the review as running: %q", name, got)
		}
	}
}
