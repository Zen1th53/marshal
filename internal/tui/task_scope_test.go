package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestTaskListsLabelTheirScope(t *testing.T) {
	st, ws, ctx := acceptanceWorkspace(t)
	if _, err := st.ImportTasks(ctx, []model.Task{{ID: "TASK-scope", Title: "scoped", Status: model.TaskReady, Risk: model.R1, Revision: 1}}); err != nil {
		t.Fatal(err)
	}
	h := &CommandHandler{ws: ws}
	for _, tc := range []struct{ line, want string }{
		{"/tasks", "scope: project, all tasks in this project"},
		{"/tasks list --scope project", "scope: project, all tasks in this project"},
		{"/tasks ownership", "WORK OWNERSHIP TABLE (scope: project"},
		{"/tasks --scope active", "no active plan: the active scope is unavailable"},
		{"/tasks ownership --scope active", "no active plan"},
		{"/tasks list --scope session", "Usage: /tasks"},
		{"/tasks --scope", "Usage: /tasks"},
		{"/tasks list extra", "Usage: /tasks"},
	} {
		got, err := h.Handle(ctx, tc.line)
		if err != nil || !strings.Contains(got, tc.want) {
			t.Errorf("%s = %q, %v; want %q", tc.line, got, err, tc.want)
		}
	}
	if got, _ := h.Handle(ctx, "/tasks"); !strings.Contains(got, "TASK-scope") {
		t.Fatalf("project scope lost a task: %s", got)
	}
}
