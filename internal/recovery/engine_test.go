package recovery

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManagerRecover(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	plan, err := mgr.Recover(ctx, "task-1", "cp-1")
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if plan.Action != ActionResumeFromCheckpoint {
		t.Fatalf("expected ActionResumeFromCheckpoint, got %s", plan.Action)
	}
	if plan.RetryCount != 1 {
		t.Fatalf("expected retry count 1, got %d", plan.RetryCount)
	}
	if len(plan.Reasons) == 0 {
		t.Fatal("expected reasons in recovery plan")
	}
}

func TestRecoveryCorruptAndPoisonedCheckpoints(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	// 1. Poisoned checkpoint -> Forces clean restart from base
	poisoned := Checkpoint{
		ID:        "cp-poisoned",
		TaskID:    "task-2",
		State:     CheckpointPoisoned,
		CreatedAt: time.Now().UTC(),
	}
	plan1, err := mgr.PlanRecovery(ctx, RecoveryRequest{
		TaskID:         "task-2",
		Checkpoint:     &poisoned,
		Failure:        FailureWorkerCrash,
		CurrentRetries: 0,
		MaxRetries:     3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan1.Action != ActionRestartFromBase {
		t.Fatalf("expected ActionRestartFromBase for poisoned checkpoint, got %s", plan1.Action)
	}

	// 2. Corrupted checkpoint -> Forces restart from base
	corrupt := Checkpoint{
		ID:        "cp-corrupt",
		TaskID:    "task-2",
		State:     CheckpointCorrupt,
		CreatedAt: time.Now().UTC(),
	}
	plan2, err := mgr.PlanRecovery(ctx, RecoveryRequest{
		TaskID:         "task-2",
		Checkpoint:     &corrupt,
		Failure:        FailureRuntimeCrash,
		CurrentRetries: 1,
		MaxRetries:     3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan2.Action != ActionRestartFromBase {
		t.Fatalf("expected ActionRestartFromBase for corrupt checkpoint, got %s", plan2.Action)
	}
	if plan2.BackoffSeconds != 2 {
		t.Fatalf("expected backoff 2 seconds for retry 2, got %d", plan2.BackoffSeconds)
	}
}

func TestRecoveryExhaustedRetries(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	_, err := mgr.PlanRecovery(ctx, RecoveryRequest{
		TaskID:         "task-3",
		Failure:        FailureWorkerCrash,
		CurrentRetries: 3,
		MaxRetries:     3,
	})
	if !errors.Is(err, ErrRetryExhausted) {
		t.Fatalf("expected ErrRetryExhausted, got %v", err)
	}
}

func TestRecoveryConcurrentOwnerConflict(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	_, err := mgr.PlanRecovery(ctx, RecoveryRequest{
		TaskID:           "task-4",
		Failure:          FailureStaleLease,
		CurrentRetries:   0,
		MaxRetries:       3,
		ActiveLeaseOwner: "agent-live-worker",
	})
	if !errors.Is(err, ErrConcurrentOwner) {
		t.Fatalf("expected ErrConcurrentOwner, got %v", err)
	}
}

func TestRecoveryExponentialBackoffSequence(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	expectedBackoffs := []int{1, 2, 4, 8, 16}
	for i, expected := range expectedBackoffs {
		plan, err := mgr.PlanRecovery(ctx, RecoveryRequest{
			TaskID:         "task-5",
			Failure:        FailureTimeout,
			CurrentRetries: i,
			MaxRetries:     10,
		})
		if err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
		if plan.BackoffSeconds != expected {
			t.Errorf("retry %d: expected backoff %d, got %d", i, expected, plan.BackoffSeconds)
		}
	}
}
