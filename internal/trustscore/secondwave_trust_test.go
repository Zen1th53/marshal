package trustscore

import (
	"testing"
)

func TestModelTaskTrustPrerequisite(t *testing.T) {
	trust := ModelTaskTrust{
		Model:    "gpt-4o",
		TaskType: "code_generation",
	}

	// 1. Initial state has no routing weight
	if trust.HasRoutingWeight {
		t.Fatalf("expected HasRoutingWeight to be false initially")
	}

	// 2. Add 9 verified outcomes -> Still < 10 tasks, no routing weight
	for i := 0; i < 9; i++ {
		trust.RecordOutcome(true)
	}
	if trust.CompletedTasks != 9 {
		t.Fatalf("expected 9 completed tasks, got %d", trust.CompletedTasks)
	}
	if trust.HasRoutingWeight {
		t.Fatalf("trust must not have routing weight before 10 tasks")
	}

	// 3. 10th task reaches the threshold
	trust.RecordOutcome(true)
	if trust.CompletedTasks != 10 {
		t.Fatalf("expected 10 completed tasks, got %d", trust.CompletedTasks)
	}
	if !trust.HasRoutingWeight {
		t.Fatalf("expected HasRoutingWeight to be true after 10 tasks")
	}
	if trust.Score != 100.0 {
		t.Fatalf("expected 100.0 score, got %f", trust.Score)
	}

	// 4. Record a contested/unverified task
	trust.RecordOutcome(false)
	if trust.CompletedTasks != 11 {
		t.Fatalf("expected 11 tasks, got %d", trust.CompletedTasks)
	}
	expectedScore := (10.0 / 11.0) * 100.0
	if trust.Score < expectedScore-0.1 || trust.Score > expectedScore+0.1 {
		t.Fatalf("unexpected score: got %f, want ~%f", trust.Score, expectedScore)
	}
}
