package execution

import (
	"context"
	"errors"
	"testing"
	"time"
)

type rejectedTaskStartStore struct {
	RunStore
	cause error
}

func (s rejectedTaskStartStore) UpdateRun(ctx context.Context, run ExecutionRun) error {
	for _, task := range run.Tasks {
		if task.Mutates && task.State == TaskRunning {
			return s.cause
		}
	}
	return s.RunStore.UpdateRun(ctx, run)
}

func TestRejectedTaskStartReleasesUnusedLease(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	engine, err := NewEngine(EngineConfig{ProjectRoot: root}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	goal, approved := createTestGoalAndPlan(time.Now().UTC())
	run, err := engine.InitializeRun(ctx, createTestHandoff(t, root, goal, approved), goal, approved)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("task start record rejected")
	engine.store = rejectedTaskStartStore{RunStore: engine.store, cause: cause}
	stopped, err := engine.ExecuteRun(ctx, run.RunID)
	if !errors.Is(err, cause) {
		t.Fatalf("start error: %v", err)
	}
	for _, task := range stopped.Tasks {
		if task.Mutates && task.LeaseID != "" {
			lease := engine.leases.leases[task.LeaseID]
			if lease == nil || lease.Status != LeaseReleased {
				t.Fatalf("rejected start retained lease: %+v", lease)
			}
			return
		}
	}
	t.Fatal("no mutation lease was acquired before the rejected start")
}
