package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rivo/uniseg"

	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

func boardTasks() []model.TaskRow {
	states := []model.TaskState{model.TaskParked, model.TaskRework, model.TaskReview, model.TaskAccepted, model.TaskWorking, model.TaskWorking, model.TaskQueued, model.TaskQueued, model.TaskQueued, model.TaskMerged, model.TaskMerged, model.TaskCancelled}
	rows := make([]model.TaskRow, len(states))
	for i, state := range states {
		rows[i] = model.TaskRow{ID: fmt.Sprintf("T-%03d", i+1), Worker: fmt.Sprintf("worker-%d", i%3+1), Scope: []string{"internal/tui/board/widgets/tasks/very-long-path.go"}, State: state, Evidence: i, EvidenceKnown: true, Returns: i % 3, ReworkLimit: 3, Since: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
	}
	return rows
}

func TestGoldenBoardTasks(t *testing.T) {
	for _, tc := range []struct {
		cols, rows int
		density    layout.Density
	}{{170, 45, layout.Full}, {100, 30, layout.Brief}} {
		t.Run(fmt.Sprint(tc.cols), func(t *testing.T) {
			pane, ok := layout.Compute(tc.cols, tc.rows, layout.Home).Pane(layout.Tasks)
			if !ok || pane.Density != tc.density {
				t.Fatalf("tasks pane: %+v %v", pane, ok)
			}
			w := Widget{SelectedID: "T-005"}
			// The widget draws a Brief pane in its compact form.
			density := pane.Density
			if density == layout.Brief {
				density = layout.Compact
			}
			got := strings.Join(w.Render(boardTasks(), pane.Rect.W, pane.Rect.H, density, time.Date(2026, 11, 2, 9, 5, 0, 0, time.UTC), anim.Frame{}, false), "\n") + "\n"
			path := filepath.Join("testdata", fmt.Sprintf("tasks-%d.golden", tc.cols))
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.WriteFile(path, []byte(got), 0600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("frame differs from %s:\n%s", path, got)
			}
		})
	}
}

func TestSelectionSurvivesProjectionAndInspects(t *testing.T) {
	w := Widget{SelectedID: "T-005"}
	rows := boardTasks()
	rows[4], rows[9] = rows[9], rows[4]
	w.Update(rows)
	if got := w.Inspect(); got != "T-005" {
		t.Fatalf("inspect %q", got)
	}
	w.Move(rows, 1)
	if got := w.Inspect(); got != "T-011" {
		t.Fatalf("next %q", got)
	}
}

func TestPulseFadesAfter300ms(t *testing.T) {
	// The animator owns pulse timing; the widget must paint its Frame faithfully.
	a := anim.New(make(chan struct{}, 1), anim.RealClock{})
	defer a.Close()
	rows := []model.TaskRow{{ID: "T-001", State: model.TaskQueued}}
	a.Update(model.Board{Tasks: rows})
	rows[0].State = model.TaskParked
	a.Update(model.Board{Tasks: rows})
	w := Widget{SelectedID: "T-001"}
	if got := strings.Join(w.Render(rows, 70, 3, layout.Full, time.Now(), a.Frame(), true), ""); !strings.Contains(got, "\x1b[1m") {
		t.Fatalf("missing pulse: %q", got)
	}
	time.Sleep(anim.Pulse + anim.Tick)
	if got := strings.Join(w.Render(rows, 70, 3, layout.Full, time.Now(), a.Frame(), true), ""); strings.Contains(got, "\x1b[1m") {
		t.Fatalf("pulse persisted: %q", got)
	}
}

func TestRowWidthAndUnknownEvidence(t *testing.T) {
	row := model.TaskRow{ID: "T-999", Worker: "worker", Scope: []string{"a/very/long/path/that/must/keep/its/suffix.go"}, State: model.TaskParked}
	w := Widget{SelectedID: row.ID}
	for _, width := range []int{1, 40, 56, 70, 100} {
		for _, color := range []bool{false, true} {
			lines := w.Render([]model.TaskRow{row}, width, 3, layout.Compact, time.Time{}, anim.Frame{}, color)
			for _, line := range lines {
				if got := uniseg.StringWidth(plain(line)); got != width {
					t.Fatalf("width %d color %v: got %d: %q", width, color, got, line)
				}
			}
			if width >= layout.MinTasks && !strings.Contains(strings.Join(lines, ""), "E:--") {
				t.Fatalf("missing unknown evidence at %d", width)
			}
		}
	}
	colored := strings.Join(w.Render([]model.TaskRow{row}, 70, 3, layout.Full, time.Time{}, anim.Frame{}, true), "\n")
	if !strings.Contains(colored, "\x1b[33m") || !strings.Contains(plain(colored), "▮ parked") || !strings.Contains(plain(colored), "…") {
		t.Fatalf("state colour, word, or left-truncated scope missing: %q", colored)
	}
}

func plain(s string) string {
	var out strings.Builder
	escape := false
	for _, r := range s {
		if r == '\x1b' {
			escape = true
			continue
		}
		if escape {
			if r == 'm' {
				escape = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
