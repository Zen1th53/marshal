package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/events"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webboard"
)

func TestBoardStartupShowsLastFinishedRun(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "board.db")
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "demo", Repository: "example/repo", DefaultBranch: "main", PackVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	run := marshal.Run{State: marshal.Closed, PlanID: "finished-plan", PlanVersion: 1, Settings: marshal.DefaultSettings()}
	for _, id := range []string{"T01", "T02", "T03", "T04"} {
		run.Tasks = append(run.Tasks, marshal.Task{PlanTaskID: id, Title: id + " finished task", State: marshal.Merged})
	}
	if _, err := st.SetMarshalRun(ctx, "demo", "older-run", run, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetMarshalRun(ctx, "demo", "finished-run", run, 0); err != nil {
		t.Fatal(err)
	}
	closedAt := time.Date(2026, 11, 21, 10, 15, 0, 0, time.UTC)
	if _, err := st.AppendMarshalDecision(ctx, events.Event{ID: "close-finished-run", Type: events.EventTypeMarshalRunClosed, RunID: "finished-run", At: closedAt, Data: map[string]any{"target": "main", "commit": "abc123"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := activeGoalBoardWorkspace(t, st, "fresh-session")
	defer w.Close()
	// The startup architecture follower reads even when this session has no goal.
	w.refreshActiveGoal(ctx)
	b := w.webBoardFrame()
	f := webboard.FromBoard(b)
	if !strings.Contains(f.NowLine.Work, "Last run: done · 4 of 4 merged") || len(f.Tasks) != 4 {
		t.Fatalf("reopened Board lost result: work=%q tasks=%d", f.NowLine.Work, len(f.Tasks))
	}
	if !f.LastRun || f.Top.Run != "finished-run" || !strings.Contains(f.NowLine.Work, closedAt.Local().Format("2006-01-02 15:04")) {
		t.Fatalf("last result metadata missing: %+v", f)
	}
	if len(f.Keys) != 0 {
		t.Fatalf("historical tasks have actions: %+v", f.Keys)
	}
	for _, task := range f.Tasks {
		if task.State != "merged" {
			t.Fatalf("task = %+v", task)
		}
	}
	if w.marshalPanel() != nil {
		t.Fatal("history became an active panel")
	}
	if !strings.Contains(homeNow(b), "Last run: done") {
		t.Fatalf("terminal home: %q", homeNow(b))
	}
	saved, err := st.GetMarshalRun(ctx, "demo", "finished-run")
	if err != nil || saved.Revision != 1 {
		t.Fatalf("history mutated run: %+v %v", saved, err)
	}
	// A new goal takes priority over history before it has tasks.
	w.mu.Lock()
	w.state.Goal = model.GoalContract{ID: "new-goal", OriginalRequest: "build something new"}
	w.mu.Unlock()
	planning := w.webBoardFrame()
	if planning.LastRun || len(planning.Tasks) != 0 {
		t.Fatal("history hid new planning")
	}
	w.mu.Lock()
	w.state.Goal = model.GoalContract{}
	w.mu.Unlock()
	// An active run takes priority over the saved result.
	active := run
	active.State = marshal.Dispatching
	active.Tasks = []marshal.Task{{PlanTaskID: "new-task", State: marshal.Queued}}
	if _, err := st.SetMarshalRun(ctx, "demo", "active-run", active, 0); err != nil {
		t.Fatal(err)
	}
	w.refreshActiveGoal(ctx)
	if w.lastRun.Load() != nil {
		t.Fatal("stored active run did not displace history")
	}
	w.setMarshalPanel(newMarshalPanel("active-run", "codex", active, ""))
	f = webboard.FromBoard(w.webBoardFrame())
	if len(f.Tasks) != 1 || f.Tasks[0].ID != "new-task" || strings.Contains(f.NowLine.Work, "Last run:") {
		t.Fatalf("active run hidden by history: %+v", f)
	}
}
