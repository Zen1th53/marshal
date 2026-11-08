package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestFileRunStoreFailedUpdatePreservesCanonicalState(t *testing.T) {
	for _, obstacle := range []string{"run.json.tmp", "run.json"} {
		t.Run(obstacle, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			store, err := NewFileRunStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.CreateRun(ctx, ExecutionRun{RunID: "run", Version: 1, Tasks: map[string]TaskExecution{"task": {TaskID: "task", AssignedHarness: "codex"}}}); err != nil {
				t.Fatal(err)
			}
			before, err := store.GetRun(ctx, "run")
			if err != nil {
				t.Fatal(err)
			}
			diskBefore, err := os.ReadFile(filepath.Join(dir, "run.json"))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, obstacle)
			if obstacle == "run.json" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(path, 0755); err != nil {
				t.Fatal(err)
			}
			updated, err := store.GetRun(ctx, "run")
			if err != nil {
				t.Fatal(err)
			}
			task := updated.Tasks["task"]
			task.AssignedHarness = "agy"
			updated.Tasks["task"] = task
			if err := store.UpdateRun(ctx, updated); err == nil {
				t.Fatal("expected injected persistence failure")
			}
			after, err := store.GetRun(ctx, "run")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("failed update changed canonical state: before=%+v after=%+v", before, after)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if obstacle == "run.json" {
				if err := os.WriteFile(path, diskBefore, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.UpdateRun(ctx, updated); err != nil {
				t.Fatalf("same revision retry failed: %v", err)
			}
			reopened, err := NewFileRunStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := reopened.GetRun(ctx, "run")
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Version != before.Version+1 || persisted.Tasks["task"].AssignedHarness != "agy" {
				t.Fatalf("retry was not durably committed: %+v", persisted)
			}
		})
	}
}

func TestFileRunStoreConcurrentCASPersistsSingleWinner(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileRunStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRun(ctx, ExecutionRun{RunID: "run"}); err != nil {
		t.Fatal(err)
	}
	run, err := store.GetRun(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	const competitors = 8
	start := make(chan struct{})
	results := make(chan error, competitors)
	var workers sync.WaitGroup
	for i := 0; i < competitors; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); <-start; results <- store.UpdateRun(ctx, run) }()
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrRunConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("got %d CAS winners", winners)
	}
	reopened, err := NewFileRunStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := reopened.GetRun(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	memory, err := store.GetRun(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Version != 2 || !reflect.DeepEqual(disk, memory) {
		t.Fatalf("disk and memory differ: disk=%+v memory=%+v", disk, memory)
	}
}

func TestFileRunStoreFailedCreateDoesNotPublishRun(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileRunStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	obstacle := filepath.Join(dir, "run.json.tmp")
	if err := os.Mkdir(obstacle, 0755); err != nil {
		t.Fatal(err)
	}
	run := ExecutionRun{RunID: "run"}
	if err := store.CreateRun(ctx, run); err == nil {
		t.Fatal("expected injected persistence failure")
	}
	if _, err := store.GetRun(ctx, run.RunID); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("failed create published run: %v", err)
	}
	if err := os.Remove(obstacle); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileRunStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reopened.GetRun(ctx, run.RunID)
	if err != nil || persisted.Version != 1 {
		t.Fatalf("initial revision not persisted consistently: %+v %v", persisted, err)
	}
}
