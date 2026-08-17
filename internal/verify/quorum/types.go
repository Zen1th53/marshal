package quorum

import (
	"errors"
	"strings"
	"time"
)

type RequirementKind string

type AttestationResult string

const (
	ResultPass AttestationResult = "PASS"
	ResultFail AttestationResult = "FAIL"
	ResultVeto AttestationResult = "VETO"
)

type Requirement struct {
	Kind             RequirementKind
	Minimum          int
	Independent      bool
	AllowedRoles     []string
	AllowedProviders []string
}

func (r Requirement) Validate() error {
	if strings.TrimSpace(string(r.Kind)) == "" || r.Minimum < 1 {
		return ErrInvalidRequirement
	}
	if len(r.AllowedRoles) == 0 && len(r.AllowedProviders) == 0 {
		return ErrInvalidRequirement
	}
	if !validList(r.AllowedRoles) || !validList(r.AllowedProviders) {
		return ErrInvalidRequirement
	}
	return nil
}

type Attestation struct {
	Subject       string
	Provider      string
	Role          string
	ChangeID      string
	EvidenceID    string
	Kind          RequirementKind
	Result        AttestationResult
	ContentDigest string
	CreatedAt     time.Time
	InvalidatedAt *time.Time
}

func (a Attestation) Validate() error {
	if strings.TrimSpace(a.Subject) == "" || strings.TrimSpace(a.Provider) == "" ||
		strings.TrimSpace(a.Role) == "" || strings.TrimSpace(a.ChangeID) == "" ||
		strings.TrimSpace(a.EvidenceID) == "" || strings.TrimSpace(string(a.Kind)) == "" ||
		!validResult(a.Result) || a.CreatedAt.IsZero() {
		return ErrInvalidAttestation
	}
	if a.InvalidatedAt != nil && a.InvalidatedAt.Before(a.CreatedAt) {
		return ErrInvalidAttestation
	}
	return nil
}

type Provenance struct {
	ChangeID      string
	ContentDigest string
}

func (p Provenance) Validate() error {
	if strings.TrimSpace(p.ChangeID) == "" || strings.TrimSpace(p.ContentDigest) == "" {
		return ErrInvalidProvenance
	}
	return nil
}

type Evaluation struct {
	State     QuorumState
	Satisfied bool
	Missing   []Requirement
	Accepted  []Attestation
	Rejected  []Attestation
}

type QuorumState string

const (
	StatePending            QuorumState = "pending"
	StatePartiallySatisfied QuorumState = "partially_satisfied"
	StateSatisfied          QuorumState = "satisfied"
	StateBlocked            QuorumState = "blocked"
	StateInvalidated        QuorumState = "invalidated"
)

var (
	ErrInvalidRequirement   = errors.New("verification requirement is invalid")
	ErrInvalidAttestation   = errors.New("verification attestation is invalid")
	ErrInvalidProvenance    = errors.New("verification provenance is invalid")
	ErrStaleAttestation     = errors.New("VERIFY_STALE_ATTESTATION")
	ErrSelfApproval         = errors.New("VERIFY_SELF_APPROVAL")
	ErrDuplicatePrincipal   = errors.New("VERIFY_DUPLICATE_PRINCIPAL")
	ErrEvidenceMissing      = errors.New("VERIFY_EVIDENCE_MISSING")
	ErrQuorumUnmet          = errors.New("VERIFY_QUORUM_UNMET")
	ErrVeto                 = errors.New("VERIFY_VETO")
	ErrAuthorityUnavailable = errors.New("VERIFY_AUTHORITY_UNAVAILABLE")
)

func validList(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return false
		}
		if _, ok := seen[value]; ok {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validResult(result AttestationResult) bool {
	switch result {
	case ResultPass, ResultFail, ResultVeto:
		return true
	default:
		return false
	}
}
