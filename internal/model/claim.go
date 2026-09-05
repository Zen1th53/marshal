package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ClaimState represents the 6 non-negotiable claim verification states.
// Strictly no PROVEN, no PARTIALLY_VERIFIED.
type ClaimState string

const (
	ClaimStateUnsupported ClaimState = "UNSUPPORTED"
	ClaimStateSupported   ClaimState = "SUPPORTED"
	ClaimStateVerified    ClaimState = "VERIFIED"
	ClaimStateContested   ClaimState = "CONTESTED"
	ClaimStateStale       ClaimState = "STALE"
	ClaimStateInvalidated ClaimState = "INVALIDATED"
)

var (
	ErrInvalidClaimState       = errors.New("invalid claim state: must be one of UNSUPPORTED, SUPPORTED, VERIFIED, CONTESTED, STALE, INVALIDATED")
	ErrPartiallyVerifiedBanned = errors.New("PARTIALLY_VERIFIED is prohibited: split an over-broad claim instead")
	ErrProvenBanned            = errors.New("PROVEN is prohibited: use VERIFIED with deterministic evidence binding")
)

func (s ClaimState) IsValid() bool {
	switch s {
	case ClaimStateUnsupported, ClaimStateSupported, ClaimStateVerified,
		ClaimStateContested, ClaimStateStale, ClaimStateInvalidated:
		return true
	default:
		return false
	}
}

// ParseClaimState parses and validates a state string, explicitly rejecting banned states.
func ParseClaimState(s string) (ClaimState, error) {
	norm := strings.ToUpper(strings.TrimSpace(s))
	switch norm {
	case "PARTIALLY_VERIFIED", "PARTIAL", "PARTIALLY-VERIFIED":
		return "", ErrPartiallyVerifiedBanned
	case "PROVEN", "PROOF":
		return "", ErrProvenBanned
	case string(ClaimStateUnsupported):
		return ClaimStateUnsupported, nil
	case string(ClaimStateSupported):
		return ClaimStateSupported, nil
	case string(ClaimStateVerified):
		return ClaimStateVerified, nil
	case string(ClaimStateContested):
		return ClaimStateContested, nil
	case string(ClaimStateStale):
		return ClaimStateStale, nil
	case string(ClaimStateInvalidated):
		return ClaimStateInvalidated, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidClaimState, s)
	}
}

// ClaimCriticality defines the stable criticality vocabulary.
// SUCCESS depends on critical-claim coverage, not evidence count.
type ClaimCriticality string

const (
	CriticalityBlocker       ClaimCriticality = "CRITICAL_BLOCKER"
	CriticalityFeature       ClaimCriticality = "CRITICAL_FEATURE"
	CriticalityStandard      ClaimCriticality = "STANDARD"
	CriticalityInformational ClaimCriticality = "INFORMATIONAL"
)

func (c ClaimCriticality) IsValid() bool {
	switch c {
	case CriticalityBlocker, CriticalityFeature, CriticalityStandard, CriticalityInformational:
		return true
	default:
		return false
	}
}

func (c ClaimCriticality) IsCritical() bool {
	return c == CriticalityBlocker || c == CriticalityFeature
}

// AuthorProvenance records the exact source of a claim or transition.
type AuthorProvenance struct {
	AgentID   string `json:"agent_id"`
	Harness   string `json:"harness"`
	Model     string `json:"model,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
}

func (a AuthorProvenance) String() string {
	parts := []string{a.AgentID}
	if a.Harness != "" {
		parts = append(parts, a.Harness)
	}
	if a.Model != "" {
		parts = append(parts, a.Model)
	}
	return strings.Join(parts, "/")
}

// EvidenceRef binds a claim to an immutable evidence record and tool metadata.
type EvidenceRef struct {
	EvidenceID      string            `json:"evidence_id"`
	EvidenceType    string            `json:"evidence_type"`
	Digest          string            `json:"digest"`
	Tool            string            `json:"tool"`
	IsDeterministic bool              `json:"is_deterministic"`
	CommitSHA       string            `json:"commit_sha,omitempty"`
	CapturedAt      time.Time         `json:"captured_at"`
	Summary         string            `json:"summary"`
	TestCoveragePct float64           `json:"test_coverage_pct,omitempty"`
	CoveredFiles    []string          `json:"covered_files,omitempty"`
	IsOracleDerived bool              `json:"is_oracle_derived,omitempty"` // Flag for verification theater detection
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// SourceCluster identifies independent sources to avoid repetition laundering.
type SourceCluster struct {
	ClusterID     string   `json:"cluster_id"`
	Origin        string   `json:"origin"`
	EvidenceIDs   []string `json:"evidence_ids"`
	IsIndependent bool     `json:"is_independent"`
}

// CodeBinding binds a claim to specific code, tests, and environment state.
type CodeBinding struct {
	CommitSHA   string   `json:"commit_sha,omitempty"`
	TreeSHA     string   `json:"tree_sha,omitempty"`
	Files       []string `json:"files,omitempty"`
	Symbols     []string `json:"symbols,omitempty"`
	Tests       []string `json:"tests,omitempty"`
	EnvVersion  string   `json:"env_version,omitempty"`
	ToolVersion string   `json:"tool_version,omitempty"`
}

// Claim represents a testable, verifiable proposition in MARSHAL.
type Claim struct {
	ID                    string           `json:"id"`
	GoalID                string           `json:"goal_id"`
	GoalRevision          int64            `json:"goal_revision"`
	Subject               string           `json:"subject"`
	NormalizedText        string           `json:"normalized_text"`
	Scope                 string           `json:"scope"`
	Criticality           ClaimCriticality `json:"criticality"`
	State                 ClaimState       `json:"state"`
	PredecessorID         string           `json:"predecessor_id,omitempty"`
	SupersedesID          string           `json:"supersedes_id,omitempty"`
	Author                AuthorProvenance `json:"author"`
	SupportingEvidence    []EvidenceRef    `json:"supporting_evidence,omitempty"`
	ContradictingEvidence []EvidenceRef    `json:"contradicting_evidence,omitempty"`
	SourceClusters        []SourceCluster  `json:"source_clusters,omitempty"`
	Binding               CodeBinding      `json:"binding"`
	StateReason           string           `json:"state_reason"`
	CreatedAt             time.Time        `json:"created_at"`
	UpdatedAt             time.Time        `json:"updated_at"`
	EvaluatedAt           time.Time        `json:"evaluated_at"`
}

// Validate checks that the claim satisfies structural invariants.
func (c Claim) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("%w: claim ID is required", ErrInvalid)
	}
	if strings.TrimSpace(c.GoalID) == "" {
		return fmt.Errorf("%w: goal ID is required", ErrInvalid)
	}
	if c.GoalRevision < 0 {
		return fmt.Errorf("%w: goal revision cannot be negative", ErrInvalid)
	}
	if strings.TrimSpace(c.Subject) == "" {
		return fmt.Errorf("%w: claim subject is required", ErrInvalid)
	}
	if strings.TrimSpace(c.NormalizedText) == "" {
		return fmt.Errorf("%w: claim normalized text is required", ErrInvalid)
	}
	if strings.TrimSpace(c.Scope) == "" {
		return fmt.Errorf("%w: claim scope is required", ErrInvalid)
	}
	if !c.Criticality.IsValid() {
		return fmt.Errorf("%w: invalid criticality %q", ErrInvalid, c.Criticality)
	}
	if !c.State.IsValid() {
		return fmt.Errorf("%w: invalid state %q", ErrInvalidClaimState, c.State)
	}
	if strings.TrimSpace(c.Author.AgentID) == "" && strings.TrimSpace(c.Author.Harness) == "" {
		return fmt.Errorf("%w: claim author provenance required", ErrInvalid)
	}
	if c.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at timestamp required", ErrInvalid)
	}
	return nil
}

// ClaimTransition records an immutable transition in a claim's epistemic lifecycle.
type ClaimTransition struct {
	TransitionID string           `json:"transition_id"`
	ClaimID      string           `json:"claim_id"`
	FromState    ClaimState       `json:"from_state"`
	ToState      ClaimState       `json:"to_state"`
	Reason       string           `json:"reason"`
	Actor        AuthorProvenance `json:"actor"`
	EvidenceRef  *EvidenceRef     `json:"evidence_ref,omitempty"`
	Timestamp    time.Time        `json:"timestamp"`
}

// Validate checks transition structure.
func (t ClaimTransition) Validate() error {
	if strings.TrimSpace(t.TransitionID) == "" {
		return fmt.Errorf("%w: transition ID required", ErrInvalid)
	}
	if strings.TrimSpace(t.ClaimID) == "" {
		return fmt.Errorf("%w: claim ID required", ErrInvalid)
	}
	if !t.FromState.IsValid() {
		return fmt.Errorf("%w: from_state invalid %q", ErrInvalidClaimState, t.FromState)
	}
	if !t.ToState.IsValid() {
		return fmt.Errorf("%w: to_state invalid %q", ErrInvalidClaimState, t.ToState)
	}
	if strings.TrimSpace(t.Reason) == "" {
		return fmt.Errorf("%w: transition reason required", ErrInvalid)
	}
	if t.Timestamp.IsZero() {
		return fmt.Errorf("%w: transition timestamp required", ErrInvalid)
	}
	return nil
}
