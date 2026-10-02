package execution

import (
	"context"
	"testing"
)

func TestRunStoreOwnsSnapshots(t *testing.T) {
	for _, fileBacked := range []bool{false, true} {
		name := "memory"
		var store RunStore = NewMemoryRunStore()
		if fileBacked {
			name = "file"
			var err error
			store, err = NewFileRunStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			run := ExecutionRun{RunID: "snapshot", Tasks: map[string]TaskExecution{"task": {TaskID: "task", State: TaskReady, Dependencies: []string{"dep"}}}}
			if err := store.CreateRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			delete(run.Tasks, "task")
			first, err := store.GetRun(ctx, run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := first.Tasks["task"]; !ok {
				t.Fatal("CreateRun retained caller's task map")
			}
			first.Tasks["task"].Dependencies[0] = "changed"
			delete(first.Tasks, "task")
			second, err := store.GetRun(ctx, run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if task, ok := second.Tasks["task"]; !ok || task.Dependencies[0] != "dep" {
				t.Fatal("GetRun exposed stored task state")
			}
			if err := store.UpdateRun(ctx, second); err != nil {
				t.Fatal(err)
			}
			delete(second.Tasks, "task")
			listed, err := store.ListRuns(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := listed[0].Tasks["task"]; !ok {
				t.Fatal("UpdateRun retained caller's task map")
			}
			delete(listed[0].Tasks, "task")
			final, err := store.GetRun(ctx, run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := final.Tasks["task"]; !ok {
				t.Fatal("ListRuns exposed stored task state")
			}
		})
	}
}
