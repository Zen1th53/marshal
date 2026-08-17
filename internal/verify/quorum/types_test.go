package quorum

import (
	"errors"
	"testing"
	"time"
)

func TestRequirementValidationRejectsMissingKind(t *testing.T) {
	requirement := Requirement{Minimum: 1, Independent: true}
	if err := requirement.Validate(); !errors.Is(err, ErrInvalidRequirement) {
		t.Fatalf("Validate() error = %v, want ErrInvalidRequirement", err)
	}
}

func TestAttestationValidationBindsChangeAndEvidence(t *testing.T) {
	attestation := Attestation{
		Subject:    "agent-a",
		Provider:   "Codex",
		Role:       "reviewer",
		ChangeID:   "change-1",
		EvidenceID: "evidence-1",
		Kind:       "security",
		Result:     ResultPass,
		CreatedAt:  time.Unix(100, 0).UTC(),
	}
	if err := attestation.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAttestationValidationRejectsUnknownResult(t *testing.T) {
	attestation := Attestation{
		Subject:    "agent-a",
		Provider:   "Codex",
		Role:       "reviewer",
		ChangeID:   "change-1",
		EvidenceID: "evidence-1",
		Kind:       "security",
		Result:     AttestationResult("ALLOW"),
		CreatedAt:  time.Unix(100, 0).UTC(),
	}
	if err := attestation.Validate(); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("Validate() error = %v, want ErrInvalidAttestation", err)
	}
}

func TestRequirementValidationRejectsDuplicateAllowedRole(t *testing.T) {
	requirement := Requirement{Kind: "security", Minimum: 2, Independent: true, AllowedRoles: []string{"reviewer", "reviewer"}}
	if err := requirement.Validate(); !errors.Is(err, ErrInvalidRequirement) {
		t.Fatalf("Validate() error = %v, want ErrInvalidRequirement", err)
	}
}
