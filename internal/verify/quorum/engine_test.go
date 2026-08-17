package quorum

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEngineEvaluateTransitionsToSatisfied(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	attestation := Attestation{
		Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "change-1",
		EvidenceID: "evidence-1", Kind: "security", Result: ResultPass,
		ContentDigest: "sha256:change", CreatedAt: created,
	}
	engine := NewEngine(func() time.Time { return created })
	result, err := engine.Evaluate(context.Background(), []Requirement{{
		Kind: "security", Minimum: 1, Independent: true, AllowedRoles: []string{"reviewer"},
	}}, []Attestation{attestation}, Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.State != StateSatisfied || !result.Satisfied || len(result.Accepted) != 1 {
		t.Fatalf("result = %+v, want satisfied with one accepted attestation", result)
	}
}

func TestEngineRejectsStaleAttestationBeforeSatisfiedState(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	engine := NewEngine(func() time.Time { return created })
	attestation := Attestation{
		Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "foreign-change",
		EvidenceID: "evidence-1", Kind: "security", Result: ResultPass,
		ContentDigest: "sha256:foreign", CreatedAt: created,
	}
	result, err := engine.Evaluate(context.Background(), []Requirement{{Kind: "security", Minimum: 1, Independent: true, AllowedRoles: []string{"reviewer"}}}, []Attestation{attestation}, Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"})
	if !errors.Is(err, ErrStaleAttestation) || result.State != StateInvalidated || result.Satisfied {
		t.Fatalf("result=%+v err=%v, want invalidated stale result", result, err)
	}
}

func TestEngineVetoBlocksQuorum(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	engine := NewEngine(func() time.Time { return created })
	attestation := Attestation{
		Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "change-1",
		EvidenceID: "evidence-1", Kind: "security", Result: ResultVeto,
		ContentDigest: "sha256:change", CreatedAt: created,
	}
	result, err := engine.Evaluate(context.Background(), []Requirement{{Kind: "security", Minimum: 1, Independent: true, AllowedRoles: []string{"reviewer"}}}, []Attestation{attestation}, Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"})
	if !errors.Is(err, ErrVeto) || result.State != StateBlocked || result.Satisfied {
		t.Fatalf("result=%+v err=%v, want blocked veto result", result, err)
	}
}
