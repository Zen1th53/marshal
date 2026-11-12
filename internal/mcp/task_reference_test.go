package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// After a run closed, the Marshal read run_digest and called task_get with the
// short name it shows ("greet"); the runtime answered "Nothing was found". The
// short plan name now resolves within the current or last run.
func TestTaskGetResolvesTheShortTaskName(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	srv := NewServer(runtime)
	caller := auth.Principal{ID: "mcp-test"}
	service := runtime.Marshal()
	if service == nil || service.ProjectID == "" {
		t.Skip("no Marshal service in this edition")
	}
	storeID := app.PlanTaskStoreID("plan", 2, "greet")
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: storeID, Title: "Greet", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	run := marshal.Run{PlanID: "plan", PlanVersion: 2, Settings: marshal.DefaultSettings(), State: marshal.Closed, Tasks: []marshal.Task{
		{PlanTaskID: "greet", Title: "Greet", Worker: "codex", State: marshal.Merged, Files: []string{"greet.py"}},
		{PlanTaskID: "docs", Title: "Write docs", Worker: "claude", State: marshal.Merged},
		{PlanTaskID: "lint-a", Title: "Lint", State: marshal.Merged},
		{PlanTaskID: "lint-b", Title: "lint", State: marshal.Merged},
	}}
	if _, err := service.Store.SetMarshalRun(ctx, service.ProjectID, "RUN-short", run, 0); err != nil {
		t.Fatal(err)
	}
	get := func(ref string) (string, error) {
		return srv.callTool(ctx, caller, "task_get", map[string]any{"task_id": ref})
	}
	for _, ref := range []string{"greet", "GREET", storeID} {
		raw, err := get(ref)
		var task model.Task
		if err != nil || json.Unmarshal([]byte(raw), &task) != nil || task.ID != storeID || task.Title != "Greet" {
			t.Fatalf("task_get %q: %s %v", ref, raw, err)
		}
	}
	// A task of the run without a store record answers what the Board shows.
	raw, err := get("docs")
	var view runTaskView
	if err != nil || json.Unmarshal([]byte(raw), &view) != nil || view.ID != "docs" || view.State != marshal.Merged || view.Worker != "claude" || view.RunID != "RUN-short" {
		t.Fatalf("task_get docs: %s %v", raw, err)
	}
	// Two tasks share the name: a plain refusal that lists both.
	_, err = get("LINT")
	if !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "lint-a (Lint)") || !strings.Contains(err.Error(), "lint-b (lint)") {
		t.Fatalf("ambiguous name: %v", err)
	}
	if msg := toolError("task_get", err).Message; !strings.HasPrefix(msg, `The arguments for task_get are not valid: "LINT" names more than one task of the last run: lint-a (Lint), lint-b (lint); pass one of these names`) {
		t.Fatalf("ambiguous message: %q", msg)
	}
	if _, err = get("nothing-like-it"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown name: %v", err)
	}
}
