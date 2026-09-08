// Package learning implements MARSHAL Process 07, which turns a bounded
// Process 06 outcome into durable, provenance-aware, revocable knowledge.
//
// Verified experience may inform future decisions. It does not become truth by
// repetition: promotion is evidence-gated, learning is advisory unless an
// explicit evidence-backed policy promotes it, and nothing here may weaken
// governance.
package learning

import (
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	// ErrInvalid marks a malformed or unbound record.
	ErrInvalid = errors.New("learning: invalid")
	// ErrBindingMismatch marks a record that does not describe the exact state
	// it is being applied to.
	ErrBindingMismatch = errors.New("learning: binding mismatch")
	// ErrTampered marks a record whose content no longer matches its digest.
	ErrTampered = errors.New("learning: tampered")
	// ErrNotPromotable marks a candidate that failed a promotion gate.
	ErrNotPromotable = errors.New("learning: not promotable")
	// ErrGovernanceVeto marks a routing proposal refused by governance.
	ErrGovernanceVeto = errors.New("learning: governance veto")
	// ErrSecretMaterial marks a candidate carrying secret or sensitive data.
	ErrSecretMaterial = errors.New("learning: secret material")
)

// Status is the Process 07 acceptance vocabulary. No other values exist.
type Status string

const (
	StatusPass    Status = "PASS"
	StatusFail    Status = "FAIL"
	StatusBlocked Status = "BLOCKED"
	StatusNotRun  Status = "NOT_RUN"
	StatusUnknown Status = "UNKNOWN"
)

func validStatus(s Status) bool {
	switch s {
	case StatusPass, StatusFail, StatusBlocked, StatusNotRun, StatusUnknown:
		return true
	}
	return false
}

// Scope separates project-local memory from generalizable memory. The default
// is project-local; generalization must be earned with evidence.
type Scope string

const (
	ScopeProject Scope = "PROJECT"
	ScopeGeneral Scope = "GENERAL"
)

// Outcome is the Process 06 completion state this learning derives from.
// Failed, partial and blocked outcomes are valuable and are ingested too.
type Outcome string

const (
	OutcomeVerifiedComplete Outcome = "VERIFIED_COMPLETE"
	OutcomePartial          Outcome = "PARTIAL"
	OutcomeFailed           Outcome = "FAILED"
	OutcomeBlocked          Outcome = "BLOCKED"
)

// Entry is the exact Process 06 binding a MemoryCommit derives from. Without a
// complete binding, learning that depends on it is BLOCKED rather than guessed.
type Entry struct {
	ProjectID           string `json:"project_id"`
	GoalID              string `json:"goal_id"`
	GoalRevision        int64  `json:"goal_revision"`
	PlanID              string `json:"plan_id"`
	PlanVersion         int64  `json:"plan_version"`
	RunID               string `json:"run_id"`
	RunVersion          int64  `json:"run_version"`
	VerificationID      string `json:"verification_id"`
	VerificationVersion int64  `json:"verification_version"`
	AttestationDigest   string `json:"attestation_digest"`
	EvidenceDigest      string `json:"evidence_bundle_digest"`
	TreeDigest          string `json:"tree_digest"`
	EnvironmentDigest   string `json:"environment_digest"`
	Outcome             Outcome
	Limitations         []string `json:"limitations,omitempty"`
	Waivers             []string `json:"waiver_ids,omitempty"`
}

// Valid reports whether the entry binds to one exact Process 06 state.
// "Task finished" is never enough: an attestation and evidence digest are
// required, and the outcome must be one MARSHAL actually produced.
func (e Entry) Valid() bool {
	if e.ProjectID == "" || e.GoalID == "" || e.PlanID == "" || e.RunID == "" || e.VerificationID == "" {
		return false
	}
	if e.GoalRevision <= 0 || e.PlanVersion <= 0 || e.RunVersion <= 0 || e.VerificationVersion <= 0 {
		return false
	}
	if e.AttestationDigest == "" || e.EvidenceDigest == "" || e.TreeDigest == "" || e.EnvironmentDigest == "" {
		return false
	}
	switch e.Outcome {
	case OutcomeVerifiedComplete, OutcomePartial, OutcomeFailed, OutcomeBlocked:
		return true
	}
	return false
}

// Dependency is something a memory item's truth rests on. When it changes, the
// item that depends on it becomes STALE rather than silently staying current.
type Dependency struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Version string `json:"version"`
}

// EvidenceRef points at the evidence that supports a claim, and records the
// cluster it came from so repetitions of one source cannot look independent.
type EvidenceRef struct {
	ID        string `json:"evidence_id"`
	ClusterID string `json:"cluster_id"`
	Digest    string `json:"digest"`
	Kind      string `json:"kind"`
	Observed  time.Time
}

// Item is one bounded claim in memory. Memory is a graph of claims and
// evidence, not a bag of prose, so every item carries its own provenance,
// scope, dependencies and freshness.
type Item struct {
	ID            string           `json:"item_id"`
	Claim         string           `json:"claim"`
	Scope         Scope            `json:"scope"`
	ProjectID     string           `json:"project_id,omitempty"`
	State         model.ClaimState `json:"state"`
	Critical      bool             `json:"critical"`
	Evidence      []EvidenceRef    `json:"evidence,omitempty"`
	Dependencies  []Dependency     `json:"dependencies,omitempty"`
	Contradicts   []string         `json:"contradicts,omitempty"`
	Applicability []string         `json:"applicability,omitempty"`
	Provenance    string           `json:"provenance"`
	Binding       Entry            `json:"binding"`
	Version       int64            `json:"version"`
	RecordedAt    time.Time
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	Superseded    string     `json:"superseded_by,omitempty"`
}

// Fresh reports whether the item is still current at now. An item with no
// expiry is not automatically fresh forever; callers pair this with dependency
// invalidation, because TTL alone is not evidence.
func (i Item) Fresh(now time.Time) bool {
	if i.State == model.ClaimStateStale || i.State == model.ClaimStateInvalidated {
		return false
	}
	if i.ExpiresAt != nil && !now.Before(*i.ExpiresAt) {
		return false
	}
	return true
}

// Commit is the canonical, versioned Process 07 record. It is inspectable,
// replayable and reversible at the metadata level.
type Commit struct {
	ID              string   `json:"memory_commit_id"`
	Binding         Entry    `json:"binding"`
	Additions       []Item   `json:"additions,omitempty"`
	Revisions       []Item   `json:"revisions,omitempty"`
	Invalidations   []string `json:"invalidations,omitempty"`
	Observations    []RoutingObservation
	Fingerprints    []Fingerprint
	Playbooks       []PlaybookCandidate
	Retention       RetentionDecision
	Provenance      string `json:"provenance"`
	CreatedAt       time.Time
	Version         int64    `json:"version"`
	Digest          string   `json:"digest"`
	BlockedLearning []string `json:"blocked_learning,omitempty"`
}

// RoutingObservation is one measured outcome for a provider on a task class.
// Every field is observed; nothing here may be invented, and Selected records
// whether the route was chosen, so selection bias stays visible.
type RoutingObservation struct {
	TaskClass        string `json:"task_class"`
	Provider         string `json:"provider"`
	ProviderVersion  string `json:"provider_version"`
	Model            string `json:"model"`
	Outcome          Outcome
	Selected         bool   `json:"selected"`
	SelectionReason  string `json:"selection_reason,omitempty"`
	Reworks          int    `json:"reworks"`
	VerifierDisputes int    `json:"verifier_disputes"`
	// CostMicros and LatencyMillis are only set when actually measured.
	// A nil pointer means not measured, which is never treated as zero.
	CostMicros    *int64 `json:"cost_micros,omitempty"`
	LatencyMillis *int64 `json:"latency_millis,omitempty"`
	EvidenceID    string `json:"evidence_id"`
	ClusterID     string `json:"cluster_id"`
	Observed      time.Time
}

// Fingerprint is a bounded, fresh record of a failure shape so the same defect
// is recognized rather than relearned.
type Fingerprint struct {
	ID           string        `json:"fingerprint_id"`
	Signature    string        `json:"signature"`
	TaskClass    string        `json:"task_class"`
	Scope        Scope         `json:"scope"`
	ProjectID    string        `json:"project_id,omitempty"`
	Occurrences  int           `json:"occurrences"`
	Evidence     []EvidenceRef `json:"evidence,omitempty"`
	Dependencies []Dependency  `json:"dependencies,omitempty"`
	FirstSeen    time.Time
	LastSeen     time.Time
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// PlaybookCandidate is a proposed reusable procedure. It is a candidate only:
// it never self-activates, and activation is an explicit governed decision.
type PlaybookCandidate struct {
	ID            string        `json:"playbook_id"`
	Title         string        `json:"title"`
	Scope         Scope         `json:"scope"`
	ProjectID     string        `json:"project_id,omitempty"`
	Steps         []string      `json:"steps"`
	Evidence      []EvidenceRef `json:"evidence,omitempty"`
	Applicability []string      `json:"applicability,omitempty"`
	// Active is always false on a candidate. Activation is out of scope for
	// Process 07 and belongs to an explicit policy decision.
	Active bool `json:"active"`
}

// RetentionDecision records what is kept, for how long, and why. Retention is
// explicit rather than "keep everything forever".
type RetentionDecision struct {
	Class     string     `json:"class"`
	KeepUntil *time.Time `json:"keep_until,omitempty"`
	Archive   bool       `json:"archive"`
	Reason    string     `json:"reason"`
}
