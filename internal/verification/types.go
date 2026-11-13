// Package verification implements the fail-closed Process 06 decision model.
// Agent statements are inputs; only evidence evaluated here can change a
// verification session or completion attestation.
package verification

import (
	"encoding/json"
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
	// Reason is the producer's own words for its status, kept so a person can
	// read why a criterion was not accepted. It is data for people; the
	// decision reads only Status.
	Reason string `json:"reason,omitempty"`
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

// RuntimeProof contains bounded observations from the runtime's integrated
// checkout and runtime check runner, not worker claims or model authority.
type RuntimeProof struct {
	ProviderObservations               []RuntimeProviderObservationProof `json:"provider_observations,omitempty"`
	Head, Base, Tree, Diff, Provenance string
	Checks                             []RuntimeCheckProof
	Honeypot                           []RuntimeHoneypotProof
}

// RuntimeProviderObservationProof binds bounded lifecycle metadata to its exact
// immutable hand-in. Observation is canonical typed JSON, never provider output.
type RuntimeProviderObservationProof struct {
	Task          string `json:"task"`
	HandInAttempt int    `json:"handin_attempt"`
	// ResultCommit is the exact immutable collected hand-in result.
	ResultCommit               string          `json:"result_commit"`
	ProviderResultCommit       string          `json:"provider_result_commit"`
	IntegratedTaskResultCommit string          `json:"integrated_task_result_commit"`
	EvidenceID                 string          `json:"evidence_id"`
	Observation                json.RawMessage `json:"observation"`
}

type RuntimeHoneypotProof struct {
	Task, EventID, ProjectID, SourceTaskID, ScanID, ResultCommit, Outcome, Timestamp string
}

// RuntimeCheckOutput references the complete immutable output, not its preview.
type RuntimeCheckOutput struct {
	ID, Digest, Path string
	Size             int64
}

type RuntimeCheckProof struct {
	OutputArtifact *RuntimeCheckOutput `json:"output_artifact,omitempty"`
	// Isolation is unknown unless observed by the runtime runner.
	Isolation                             string `json:"isolation,omitempty"`
	ID, Task, Head, Tree, Command, Output string
	Criteria                              []string
	ExitCode                              int
}

type Session struct {
	RuntimeProof         *RuntimeProof     `json:"runtime_proof,omitempty"`
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
