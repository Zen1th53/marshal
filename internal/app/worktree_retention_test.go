package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/worktree"
)

func TestRuntimeGCRetainsCanonicalArtifactsBeyondTTL(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	wm := worktree.New(r.layout.Root, r.layout.Worktrees)
	names := []string{"TASK-retained-a", "TASK-retained-r2-integration", "TASK-retained-r1-old", "TASK-lane", "TASK-active", "TASK-abandoned"}
	for _, id := range names {
		if _, err := wm.Prepare(t.Context(), model.WorktreeRequest{TaskID: id, Branch: "gc/" + id, BaseCommit: r.layout.HEAD}); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-72 * time.Hour)
		if err := os.Chtimes(filepath.Join(r.layout.Worktrees, id), old, old); err != nil {
			t.Fatal(err)
		}
	}
	run := marshal.Run{PlanID: "gc-plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Reviewing, Tasks: []marshal.Task{{PlanTaskID: "a", State: marshal.HandedIn}}, Operation: &marshal.LifecycleOperation{Dir: filepath.Join(r.layout.Worktrees, "TASK-retained-a"), Kind: "merge"}}
	if _, err := r.store.SetMarshalRun(t.Context(), r.ProjectID(), "retained", run, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ImportTasks(t.Context(), []model.Task{{ID: "TASK-active", Title: "active", Status: model.TaskWorking, Risk: model.R1}, {ID: "TASK-abandoned", Title: "abandoned", Status: model.TaskCancelled, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	execRoot := filepath.Join(r.layout.RuntimeDir, "execution", "runs")
	if err := os.MkdirAll(execRoot, 0700); err != nil {
		t.Fatal(err)
	}
	lane := execution.ExecutionRun{RunID: "lane", Tasks: map[string]execution.TaskExecution{"a": {TaskID: "a", WorktreePath: filepath.Join(r.layout.Worktrees, "TASK-lane")}}}
	body, err := json.Marshal(lane)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execRoot, "lane.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{true, false} {
		got, err := r.GCWorktrees(t.Context(), dry, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Errors) > 0 || len(got.CleanedPaths) != 1 || filepath.Base(got.CleanedPaths[0]) != "TASK-abandoned" {
			t.Fatalf("GC dry=%t: %+v", dry, got)
		}
		for _, id := range names[:len(names)-1] {
			path := filepath.Join(r.layout.Worktrees, id)
			if !slices.Contains(got.RetainedPaths, path) {
				t.Fatalf("lost canonical retention %s: %+v", id, got)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestRuntimeGCStopsOnUnreadableCanonicalReferences(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	path := filepath.Join(r.layout.RuntimeDir, "execution", "runs")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "broken.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GCWorktrees(t.Context(), false, time.Nanosecond); err == nil {
		t.Fatal("GC ignored unknown canonical retention")
	}
}
