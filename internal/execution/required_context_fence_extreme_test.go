//go:build !governed

package execution

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRequiredContextFencePinsParentAndLaneAndSerializesWriter(t *testing.T) {
	for _, kind := range []string{"memory", "file"} {
		t.Run(kind, func(t *testing.T) {
			var s RunStore = NewMemoryRunStore()
			if kind == "file" {
				file, err := NewFileRunStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				s = file
			}
			for _, id := range []string{"parent", "lane"} {
				if err := s.CreateRun(t.Context(), ExecutionRun{RunID: id, Version: 1, State: RunRunning, Tasks: map[string]TaskExecution{"task": {TaskID: "task", Description: "immutable"}}}); err != nil {
					t.Fatal(err)
				}
			}
			f := s.(requiredContextFenceStore)
			for _, versions := range []map[string]int64{{"parent": 2, "lane": 1}, {"parent": 1, "missing": 1}, {}} {
				called := false
				err := f.WithRequiredContextFence(t.Context(), versions, func(map[string]ExecutionRun) error { called = true; return nil })
				if err == nil || called {
					t.Fatal("stale/incomplete fence ran protected effect", err)
				}
			}
			writerStarted, writerDone := make(chan struct{}), make(chan error, 1)
			current, err := s.GetRun(t.Context(), "lane")
			if err != nil {
				t.Fatal(err)
			}
			err = f.WithRequiredContextFence(t.Context(), map[string]int64{"parent": 1, "lane": 1}, func(rows map[string]ExecutionRun) error {
				if rows["parent"].Version != 1 || rows["lane"].Version != 1 {
					t.Fatal("fence did not pin complete snapshots")
				}
				go func() { close(writerStarted); writerDone <- s.UpdateRun(t.Context(), current) }()
				<-writerStarted
				select {
				case <-writerDone:
					t.Fatal("writer changed canonical run during protected effect")
				case <-time.After(20 * time.Millisecond):
				}
				row := rows["lane"]
				row.Tasks["task"] = TaskExecution{TaskID: "task", Description: "caller mutation"}
				rows["lane"] = row
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-writerDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("short fence retained writer lock")
			}
			row, err := s.GetRun(t.Context(), "lane")
			if err != nil || row.Version != 2 || row.Tasks["task"].Description != "immutable" {
				t.Fatal("callback mutated canonical state", row, err)
			}
			refused := errors.New("local admission refused")
			if err = f.WithRequiredContextFence(t.Context(), map[string]int64{"parent": 1, "lane": 2}, func(map[string]ExecutionRun) error { return refused }); !errors.Is(err, refused) {
				t.Fatal("admission refusal lost", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			called := false
			if err = f.WithRequiredContextFence(ctx, map[string]int64{"parent": 1, "lane": 2}, func(map[string]ExecutionRun) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
				t.Fatal("cancelled fence admitted", err)
			}
		})
	}
}
