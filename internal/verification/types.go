// Package verification implements the fail-closed Process 06 decision model.
// Agent statements are inputs; only evidence evaluated here can change a
// verification session or completion attestation.
package verification

import (
	"errors"
	"time"
)

var (
	ErrInvalid          = errors.New("invalid verification record")
	ErrConflict         = errors.New("verification CAS conflict")
	ErrNotFound         = errors.New("verification record not found")
	ErrBindingMismatch  = errors.New("verification binding mismatch")
	ErrTampered         = errors.New("verification digest mismatch")
	ErrUnsafeWaiver     = errors.New("critical evidence cannot be waived")
	ErrReplayDivergence = errors.New("evidence replay diverged")
)

type Status string

const (
	StatusPass    Status = "PASS"
	StatusFail    Status = "FAIL"
	StatusBlocked Status = "BLOCKED"
	StatusNotRun  Status = "NOT_RUN"
	StatusUnknown Status = "UNKNOWN"
)

type Decision string

const (
	VerifiedComplete   Decision = "VERIFIED_COMPLETE"
	PartiallySatisfied Decision = "PARTIALLY_SATISFIED"
	VerificationFailed Decision = "VERIFICATION_FAILED"
	Blocked            Decision = "BLOCKED"
	NeedsReexecution   Decision = "NEEDS_REEXECUTION"
	NeedsReplan        Decision = "NEEDS_REPLAN"
	Cancelled          Decision = "CANCELLED"
)

type Binding struct {
	ProjectID, GoalID, PlanID, RunID      string
	GoalRevision, PlanVersion, RunVersion int64
	TreeDigest, EnvironmentDigest         string
}

type Criterion struct {
	ID        string   `json:"id"`
	Mandatory bool     `json:"mandatory"`
	ClaimIDs  []string `json:"claim_ids"`
}

type Claim struct {
	ID            string   `json:"id"`
	CriterionID   string   `json:"criterion_id"`
	Critical      bool     `json:"critical"`
	SemanticScope []string `json:"semantic_scope"`
	EvidenceIDs   []string `json:"evidence_ids"`
}

type Evidence struct {
	ID                                    string `json:"id"`
	ClaimID                               string `json:"claim_id"`
	Kind                                  string `json:"kind"`
	Status                                Status `json:"status"`
	ContentDigest                         string `json:"content_digest"`
	TreeDigest                            string `json:"tree_digest"`
	EnvironmentDigest                     string `json:"environment_digest"`
	Producer, Provider, Oracle, ClusterID string
	Command                               []string `json:"command,omitempty"`
	InputsDigest, OutputDigest            string
	Attempts, Passes                      int
	CreatedAt                             time.Time  `json:"created_at"`
	ExpiresAt                             *time.Time `json:"expires_at,omitempty"`
}

type Contradiction struct {
	ID       string
	ClaimID  string
	Critical bool
	Resolved bool
	Detail   string
}
type Waiver struct {
	ID, CriterionID, Actor, Reason string
	ExpiresAt                      time.Time
	Critical                       bool
}

type Session struct {
	ID                   string            `json:"verification_id"`
	Version              int64             `json:"version"`
	Binding              Binding           `json:"binding"`
	State                Decision          `json:"state"`
	Criteria             []Criterion       `json:"criteria"`
	Claims               []Claim           `json:"claims"`
	Evidence             []Evidence        `json:"evidence"`
	Contradictions       []Contradiction   `json:"contradictions,omitempty"`
	Waivers              []Waiver          `json:"waivers,omitempty"`
	RequiredChecks       map[string]Status `json:"required_checks"`
	KnownBlockers        []string          `json:"known_blockers,omitempty"`
	Limitations          []string          `json:"limitations,omitempty"`
	RiskTier             string            `json:"risk_tier"`
	Cancelled            bool              `json:"cancelled"`
	CreatedAt, UpdatedAt time.Time
}

type CompletionAttestation struct {
	ID                                                 string  `json:"attestation_id"`
	Binding                                            Binding `json:"binding"`
	VerificationID                                     string  `json:"verification_id"`
	VerificationVersion                                int64   `json:"verification_version"`
	CriteriaDigest, ClaimsDigest, EvidenceBundleDigest string
	Decision                                           Decision  `json:"decision"`
	Limitations                                        []string  `json:"limitations,omitempty"`
	WaiverIDs                                          []string  `json:"waiver_ids,omitempty"`
	IssuedAt                                           time.Time `json:"issued_at"`
	Provenance                                         string    `json:"provenance"`
	Digest                                             string    `json:"digest"`
}

type IntegrationAttestation struct {
	CandidateSHA, MainSHA, MergeTreeDigest, EvidenceBundleDigest string
	CandidateIsAncestor                                          bool
	CriticalGates                                                map[string]Status
	Checks                                                       []string
	Gaps                                                         []string
	IssuedAt                                                     time.Time
	Digest                                                       string
}
