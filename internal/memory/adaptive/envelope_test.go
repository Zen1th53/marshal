package adaptive_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/adaptive"
)

type mockLearnedPolicy struct {
	proposedAction adaptive.MemoryAction
	fingerprint    string
}

func (m *mockLearnedPolicy) ProposeAction(ctx context.Context, state adaptive.TaskState) (adaptive.MemoryAction, string) {
	return m.proposedAction, m.fingerprint
}

func TestT144LearnedMemoryPolicySafetyEnvelope(t *testing.T) {
	ctx := context.Background()

	// 1. Unsafe learned proposal (e.g. direct durable promotion bypass) is strictly DENIED
	unsafePolicy := &mockLearnedPolicy{
		proposedAction: adaptive.MemoryAction{
			Type:   "FORCE_DURABLE_PROMOTION",
			Reason: "Learned policy hallucinated direct promotion privilege",
		},
		fingerprint: "policy-v1-unsafe",
	}

	envelope := adaptive.NewSafetyEnvelope(unsafePolicy, false) // Active mode
	_, err := envelope.ExecuteProposal(ctx, adaptive.TaskState{TaskID: "TASK-1"})
	if !errors.Is(err, adaptive.ErrUnsafeLearnedProposal) {
		t.Fatalf("expected ErrUnsafeLearnedProposal for unauthorized learned action, got: %v", err)
	}

	// 2. Safe proposal executes in active mode
	safePolicy := &mockLearnedPolicy{
		proposedAction: adaptive.MemoryAction{
			Type:   adaptive.ActionRecall,
			Reason: "Learned policy recommends recall for complex task",
		},
		fingerprint: "policy-v1-safe",
	}

	envelopeSafe := adaptive.NewSafetyEnvelope(safePolicy, false)
	act, err := envelopeSafe.ExecuteProposal(ctx, adaptive.TaskState{TaskID: "TASK-2"})
	if err != nil || act.Type != adaptive.ActionRecall {
		t.Fatalf("expected successful execution of safe proposal, got: %+v (err: %v)", act, err)
	}

	// 3. Shadow mode: records decision diff without applying active mutation
	envelopeShadow := adaptive.NewSafetyEnvelope(safePolicy, true) // Shadow mode
	shadowDiff, err := envelopeShadow.ExecuteShadow(ctx, adaptive.TaskState{TaskID: "TASK-3"}, adaptive.MemoryAction{Type: adaptive.ActionNoOp})
	if err != nil {
		t.Fatalf("ExecuteShadow: %v", err)
	}
	if !shadowDiff.ShadowMode || shadowDiff.ActiveAction.Type != adaptive.ActionNoOp || shadowDiff.ProposedAction.Type != adaptive.ActionRecall {
		t.Fatalf("unexpected shadow diff: %+v", shadowDiff)
	}
}
