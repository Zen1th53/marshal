package trust_test

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/trust"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT90ExplainableScoreBreakdown(t *testing.T) {
	scorer := trust.NewScorer()
	now := time.Now().UTC()

	// 1. Evidence-backed durable record vs Unverified agent candidate
	recVerified := model.MemoryRecordV2{
		ID:          "MEM-V",
		Kind:        model.MemoryKindSemantic,
		Lifecycle:   model.MemoryDurable,
		Authority:   model.AuthorityVerified,
		Confidence:  model.ConfidenceVerified,
		ObservedAt:  now.Add(-time.Hour),
		ValidFrom:   now.Add(-time.Hour),
		EvidenceIDs: []string{"EVID-1", "EVID-2"},
	}

	recUnverified := model.MemoryRecordV2{
		ID:         "MEM-U",
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryCandidate,
		Authority:  model.AuthorityAgent,
		Confidence: model.ConfidenceInferred,
		ObservedAt: now.Add(-time.Hour),
		ValidFrom:  now.Add(-time.Hour),
	}

	breakdownV := scorer.Score(recVerified, now)
	breakdownU := scorer.Score(recUnverified, now)

	if breakdownV.FinalScore <= breakdownU.FinalScore {
		t.Fatalf("expected verified score (%f) > unverified score (%f)", breakdownV.FinalScore, breakdownU.FinalScore)
	}

	if len(breakdownV.Reasons) == 0 {
		t.Fatal("expected machine-readable reasons in breakdown")
	}

	// 2. Conflicted record receives heavy penalty
	recConflicted := recVerified
	recConflicted.Lifecycle = model.MemoryConflicted
	breakdownC := scorer.Score(recConflicted, now)
	if breakdownC.FinalScore >= breakdownV.FinalScore {
		t.Fatalf("conflicted record should have lower score than durable: %f >= %f", breakdownC.FinalScore, breakdownV.FinalScore)
	}
	if breakdownC.LifecyclePenalty <= 0 {
		t.Fatalf("expected lifecycle penalty for conflicted record, got %f", breakdownC.LifecyclePenalty)
	}
}

func TestT90ProviderIdentityCannotBypassAuthority(t *testing.T) {
	scorer := trust.NewScorer()
	now := time.Now().UTC()

	// Agent record that claims to be from a powerful provider (e.g. gpt-4o, claude-3-5)
	// Still has AuthorityAgent and Candidate lifecycle
	recAgent := model.MemoryRecordV2{
		ID:         "MEM-ADV-01",
		Authority:  model.AuthorityAgent,
		Lifecycle:  model.MemoryCandidate,
		ObservedAt: now,
		ValidFrom:  now,
		Source: model.MemorySource{
			Kind:      "external",
			Reference: "claude-3-5-sonnet",
		},
	}

	breakdown := scorer.Score(recAgent, now)
	if breakdown.AuthorityScore > 0.50 {
		t.Fatalf("provider label should not inflate authority score, got %f", breakdown.AuthorityScore)
	}
}
