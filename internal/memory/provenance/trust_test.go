package provenance_test

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/provenance"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT83DerivedTrustScore(t *testing.T) {
	evaluator := provenance.NewTrustEvaluator()

	now := time.Now().UTC()

	// 1. Evidence-backed, operator-authorized memory has high trust
	highTrustRec := model.MemoryRecordV2{
		ID:          "MEM-TRUST-01",
		Kind:        model.MemoryKindDecision,
		Lifecycle:   model.MemoryDurable,
		Authority:   model.AuthorityOperator,
		ObservedAt:  now.Add(-time.Hour),
		ValidFrom:   now.Add(-time.Hour),
		EvidenceIDs: []string{"EVID-001", "EVID-002"},
	}

	scoreHigh := evaluator.EvaluateTrust(highTrustRec, now)
	if scoreHigh < 0.85 {
		t.Fatalf("expected high trust score (>=0.85), got %f", scoreHigh)
	}

	// 2. Untrusted single-agent assertion without evidence has low trust
	lowTrustRec := model.MemoryRecordV2{
		ID:          "MEM-TRUST-02",
		Kind:        model.MemoryKindSemantic,
		Lifecycle:   model.MemoryCandidate,
		Authority:   model.AuthorityAgent,
		ObservedAt:  now.Add(-24 * time.Hour),
		ValidFrom:   now.Add(-24 * time.Hour),
		EvidenceIDs: nil, // no evidence
	}

	scoreLow := evaluator.EvaluateTrust(lowTrustRec, now)
	if scoreLow > 0.50 {
		t.Fatalf("expected low trust score (<=0.50), got %f", scoreLow)
	}

	if scoreHigh <= scoreLow {
		t.Fatalf("expected high trust (%f) > low trust (%f)", scoreHigh, scoreLow)
	}

	// 3. Stale record trust decay
	staleRec := highTrustRec
	staleRec.ObservedAt = now.Add(-365 * 24 * time.Hour) // 1 year old
	scoreStale := evaluator.EvaluateTrust(staleRec, now)
	if scoreStale >= scoreHigh {
		t.Fatalf("stale record did not decay in trust: %f >= %f", scoreStale, scoreHigh)
	}
}

func TestT83ProtectedPromotionRequiresEvidence(t *testing.T) {
	evaluator := provenance.NewTrustEvaluator()

	// Decision record with no EvidenceIDs cannot be verified/promoted to protected status
	rec := model.MemoryRecordV2{
		ID:          "MEM-PROT-01",
		Kind:        model.MemoryKindDecision,
		Lifecycle:   model.MemoryCandidate,
		Authority:   model.AuthorityPolicy,
		EvidenceIDs: nil, // Missing evidence
	}

	err := evaluator.ValidateProtectedPromotion(rec)
	if err == nil {
		t.Fatal("expected error for protected promotion without evidence IDs")
	}
}
