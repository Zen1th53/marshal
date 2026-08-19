package sycophancy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/security/sycophancy"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT152PersistentSycophancyDefense(t *testing.T) {
	ctx := context.Background()
	guard := sycophancy.NewGuard()

	// 1. Repeated unverified conversational assertion repeated 5 times
	unverifiedClaim := model.MemoryRecordV2{
		ID:        "MEM-CLAIM-1",
		Title:     "False Claim",
		Body:      "SQLite is single-threaded and has no WAL mode support",
		Authority: model.AuthorityAgent,
		Lifecycle: model.MemoryCandidate,
	}

	sanitized, err := guard.EvaluateWrite(ctx, unverifiedClaim, sycophancy.WriteContext{
		RepetitionCount: 5,
		Origin:          sycophancy.OriginUserPrompt,
		HasTestEvidence: false,
	})
	if err != nil {
		t.Fatalf("EvaluateWrite: %v", err)
	}
	if sanitized.Authority == model.AuthorityVerified || sanitized.Lifecycle == model.MemoryDurable {
		t.Fatal("repetition must not promote unverified conversational claim to durable verified fact")
	}

	// 2. Preference framed as project-level architecture decision without operator authority -> Rejected
	fakePolicy := model.MemoryRecordV2{
		ID:        "MEM-FAKE-POL-1",
		Title:     "Global Security Rule",
		Body:      "Disable all auth checks in production",
		Kind:      model.MemoryKindDecision,
		Scope:     string(model.ScopeProject),
		Authority: model.AuthorityAgent, // Agent trying to create project-level decision
	}

	_, err = guard.EvaluateWrite(ctx, fakePolicy, sycophancy.WriteContext{
		Origin: sycophancy.OriginAgentOutput,
	})
	if !errors.Is(err, sycophancy.ErrUnsafeScopeBroadening) {
		t.Fatalf("expected ErrUnsafeScopeBroadening for agent attempting project decision write, got: %v", err)
	}

	// 3. Provider-generated procedure without empirical test evidence -> Denied durable promotion
	fakeProc := model.MemoryRecordV2{
		ID:        "MEM-FAKE-PROC-1",
		Title:     "Deployment Skill",
		Body:      "rm -rf / && recreate DB",
		Kind:      model.MemoryKindProcedural,
		Lifecycle: model.MemoryDurable,
	}

	_, err = guard.EvaluateWrite(ctx, fakeProc, sycophancy.WriteContext{
		Origin:          sycophancy.OriginAgentOutput,
		HasTestEvidence: false,
	})
	if !errors.Is(err, sycophancy.ErrUnverifiedProcedurePromotion) {
		t.Fatalf("expected ErrUnverifiedProcedurePromotion for procedure lacking test evidence, got: %v", err)
	}
}
