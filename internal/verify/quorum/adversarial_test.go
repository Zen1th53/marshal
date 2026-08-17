package quorum

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestEngineRejectsDuplicatePrincipalEvenAcrossProviders(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	engine := NewEngine(func() time.Time { return created })
	base := Attestation{Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "change-1", EvidenceID: "evidence-1", Kind: "security", Result: ResultPass, ContentDigest: "sha256:change", CreatedAt: created}
	second := base
	second.Provider = "Claude"
	second.EvidenceID = "evidence-2"
	_, err := engine.Evaluate(context.Background(), []Requirement{{Kind: "security", Minimum: 2, Independent: true, AllowedRoles: []string{"reviewer"}}}, []Attestation{base, second}, Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"})
	if !errors.Is(err, ErrDuplicatePrincipal) {
		t.Fatalf("error=%v, want ErrDuplicatePrincipal", err)
	}
}

func TestPassWithoutEvidenceIDIsRejected(t *testing.T) {
	attestation := Attestation{Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "change-1", Kind: "security", Result: ResultPass, ContentDigest: "sha256:change", CreatedAt: time.Unix(100, 0).UTC()}
	if err := attestation.Validate(); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("error=%v, want ErrInvalidAttestation", err)
	}
}

func FuzzRequirementValidationNoPanic(f *testing.F) {
	f.Add("security", 1, true, "reviewer")
	f.Fuzz(func(t *testing.T, kind string, minimum int, independent bool, role string) {
		_ = (Requirement{Kind: RequirementKind(kind), Minimum: minimum, Independent: independent, AllowedRoles: []string{role}}).Validate()
	})
}

func TestConcurrentEvaluationIsDeterministic(t *testing.T) {
	created := time.Unix(100, 0).UTC()
	engine := NewEngine(func() time.Time { return created })
	requirements := []Requirement{{Kind: "security", Minimum: 1, Independent: true, AllowedRoles: []string{"reviewer"}}}
	attestations := []Attestation{{Subject: "agent-a", Provider: "Codex", Role: "reviewer", ChangeID: "change-1", EvidenceID: "evidence-1", Kind: "security", Result: ResultPass, ContentDigest: "sha256:change", CreatedAt: created}}
	provenance := Provenance{ChangeID: "change-1", ContentDigest: "sha256:change"}
	const contenders = 32
	var wg sync.WaitGroup
	errCh := make(chan error, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := engine.Evaluate(context.Background(), requirements, attestations, provenance)
			if err != nil || result.State != StateSatisfied || len(result.Accepted) != 1 || result.Accepted[0].EvidenceID != "evidence-1" {
				errCh <- errors.New("concurrent evaluation diverged")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}
