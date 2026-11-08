package inspector

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

func TestTimelineToggleAndRunBoundary(t *testing.T) {
	task := marshal.Task{PlanTaskID: "T-1", Worker: "codex", State: marshal.Escalated}
	b := model.Project(model.Snapshot{RunID: "R1", Run: marshal.Run{Tasks: []marshal.Task{task}}}, []events.Event{
		{ID: "approval", Sequence: 1, RunID: "R1", Type: events.EventTypeMarshalPlanApproved},
		{ID: "dispatch", Sequence: 2, RunID: "R1", TaskID: "T-1", Type: events.EventTypeMarshalTaskDispatched},
		{ID: "charge", Sequence: 3, RunID: "R1", TaskID: "T-1", Type: events.EventTypeMarshalUsageCharged},
		{ID: "park", Sequence: 4, RunID: "R1", TaskID: "T-1", Type: events.EventTypeMarshalEscalated, Data: map[string]any{"reason": "scope blocked"}},
		{ID: "foreign", Sequence: 5, RunID: "R2", TaskID: "T-1", Type: events.EventTypeMarshalTaskReturned},
	})
	v := New(b, "T-1")
	got := strings.Join(v.Render(76, 30), "\n")
	if strings.Contains(got, "usage.charged") || strings.Contains(got, "foreign") || !strings.Contains(got, "scope blocked") || !strings.Contains(got, "dispatch") {
		t.Fatalf("default timeline: %s", got)
	}
	v.Key('e')
	got = strings.Join(v.Render(76, 30), "\n")
	if !strings.Contains(got, "usage.charged") || strings.Contains(got, "foreign") {
		t.Fatalf("all events: %s", got)
	}
}

func TestReassignedTaskBreadcrumbsAndNavigation(t *testing.T) {
	r := marshal.Run{PlanID: "P1", Tasks: []marshal.Task{{PlanTaskID: "T-1", ParentID: "T-0", Worker: "claude", State: marshal.Reassigned}}}
	b := model.Project(model.Snapshot{RunID: "R1", Run: r}, nil)
	v := New(b, "T-1")
	if got := strings.Join(v.Breadcrumbs(), " › "); got != "R1 › P1 › T-0 › T-1 › claude" {
		t.Fatalf("breadcrumbs = %q", got)
	}
	// T-0 is not on the Board and T-1 has no child: neither key moves,
	// and the hint offers neither.
	if v.Key('\n') || v.Key('\b') || v.Depth() != 0 || v.TaskID() != "T-1" {
		t.Fatalf("navigation without a chain moved: depth %d task %s", v.Depth(), v.TaskID())
	}
	got := strings.Join(v.Render(80, 30), "\n")
	if strings.Contains(got, "Enter") || strings.Contains(got, "Backspace") || !strings.Contains(got, "e all events · Esc close") {
		t.Fatalf("hint shows keys that do nothing:\n%s", got)
	}
}

func TestBackspaceWalksToParentTask(t *testing.T) {
	r := marshal.Run{PlanID: "P1", Tasks: []marshal.Task{
		{PlanTaskID: "T-0", Worker: "agy", State: marshal.Dispatched},
		{PlanTaskID: "T-1", ParentID: "T-0", Worker: "claude", State: marshal.Reassigned},
		{PlanTaskID: "T-2", ParentID: "T-1", Worker: "codex", State: marshal.Queued},
	}}
	v := New(model.Project(model.Snapshot{RunID: "R1", Run: r}, nil), "T-1")
	if got := strings.Join(v.Render(80, 30), "\n"); !strings.Contains(got, "e all events · Enter T-2 · Backspace T-0 · Esc close") {
		t.Fatalf("hint:\n%s", got)
	}
	if !v.Key('\b') || v.Depth() != -1 || v.TaskID() != "T-0" || !strings.Contains(strings.Join(v.Render(80, 24), "\n"), "T-0") {
		t.Fatalf("Backspace did not open parent task: %v", v.Breadcrumbs())
	}
	if v.Key('\b') {
		t.Fatal("Backspace at the top of the chain was taken; the caller closes there")
	}
	if !v.Key('\n') || v.Depth() != 0 || v.TaskID() != "T-1" {
		t.Fatalf("Enter did not return to child task: %v", v.Breadcrumbs())
	}
	if !v.Key('\n') || v.Depth() != 1 || v.TaskID() != "T-2" {
		t.Fatalf("Enter did not walk down to the delegated task: %v", v.Breadcrumbs())
	}
	if v.Key('\n') {
		t.Fatal("Enter on a leaf was taken")
	}
	// The view keeps its place when the Board is projected again.
	v.SetBoard(model.Project(model.Snapshot{RunID: "R1", Run: r}, nil))
	if v.TaskID() != "T-2" {
		t.Fatalf("SetBoard lost the navigation: %s", v.TaskID())
	}
}

func TestParkedTaskSections(t *testing.T) {
	r := marshal.Run{PlanID: "P1", Budget: marshal.Budget{Tokens: marshal.Ceiling{Task: 100}}, Tasks: []marshal.Task{{PlanTaskID: "T-1", Title: "Ship", Worker: "codex", State: marshal.Escalated, Files: []string{"internal/tui/board"}, Checks: []marshal.Check{{Command: "go test ./internal/tui/..."}}}}}
	b := model.Project(model.Snapshot{RunID: "R1", Run: r}, []events.Event{{ID: "park", Sequence: 1, RunID: "R1", TaskID: "T-1", Type: events.EventTypeMarshalEscalated, At: time.Unix(1, 0), Data: map[string]any{"reason": "needs approval"}}})
	got := strings.Join(New(b, "T-1").Render(76, 30), "\n")
	for _, s := range []string{"IDENTITY", "AUTHORITY", "EVIDENCE", "TIMELINE", "ACTIONS", "parked", "needs approval", "internal/tui/board", "go test ./internal/tui/..."} {
		if !strings.Contains(got, s) {
			t.Errorf("frame missing %q:\n%s", s, got)
		}
	}
}

func TestActionsOnlyWhatApplies(t *testing.T) {
	r := marshal.Run{Tasks: []marshal.Task{{PlanTaskID: "T-1", Worker: "codex", State: marshal.Escalated}, {PlanTaskID: "T-2", Worker: "agy", State: marshal.Escalated}}}
	b := model.Project(model.Snapshot{RunID: "R1", Run: r}, nil)
	if got := New(b, "T-1").Actions(); len(got) != 0 {
		t.Fatalf("no decision, no pane: %v", got)
	}
	egress := model.InboxItem{Kind: model.InboxEgress, Gate: model.GateSingle}
	egress.Request.TaskID, egress.Request.Object = "T-1", "example.com:443"
	gate := model.InboxItem{Kind: model.InboxGate, Gate: model.GateTyped, Canonical: "task.T-2.push"}
	gate.Request.TaskID = "T-2"
	b.Inbox = []model.InboxItem{egress, gate}
	b.Lanes = []model.Lane{{Worker: "codex-1", TaskID: "T-1", Pane: "%3"}}
	keys := func(as []Action) string {
		s := ""
		for _, a := range as {
			s += string(a.Key)
		}
		return s
	}
	if got := keys(New(b, "T-1").Actions()); got != "Aadrxo" {
		t.Fatalf("decision and pane: %q", got)
	}
	// A hard gate has no allow-for-run, as the inbox offers none.
	if got := keys(New(b, "T-2").Actions()); got != "adrx" {
		t.Fatalf("hard gate: %q", got)
	}
	rows, buttons := ActionRows(New(b, "T-1").Actions(), 40)
	if len(rows) < 2 {
		t.Fatalf("40 cells did not wrap: %q", rows)
	}
	for _, btn := range buttons {
		line := []rune(rows[btn.Row])
		if btn.X1 > 40 || string(line[btn.X0:btn.X1]) != "["+string(btn.Key)+"] "+btn.Label {
			t.Fatalf("button %c at %d..%d on %q", btn.Key, btn.X0, btn.X1, rows[btn.Row])
		}
	}
}
