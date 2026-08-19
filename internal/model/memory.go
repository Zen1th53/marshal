package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// MemoryKind classifies what a memory record represents.
// Zero value is invalid at validation boundaries.
type MemoryKind string

const (
	MemoryKindWorking    MemoryKind = "working"
	MemoryKindSemantic   MemoryKind = "semantic"
	MemoryKindEpisodic   MemoryKind = "episodic"
	MemoryKindDecision   MemoryKind = "decision"
	MemoryKindProcedural MemoryKind = "procedural"
	MemoryKindFinding    MemoryKind = "finding"
	MemoryKindHandoff    MemoryKind = "handoff"
	MemoryKindCheckpoint MemoryKind = "checkpoint"
	MemoryKindFailure    MemoryKind = "failure"
)

func (k MemoryKind) IsValid() bool {
	switch k {
	case MemoryKindWorking, MemoryKindSemantic, MemoryKindEpisodic,
		MemoryKindDecision, MemoryKindProcedural, MemoryKindFinding,
		MemoryKindHandoff, MemoryKindCheckpoint, MemoryKindFailure:
		return true
	}
	return false
}

// MemoryLifecycle represents the required lifecycle path from spec §4.
// Zero value is invalid at validation boundaries.
type MemoryLifecycle string

const (
	// Forward path
	MemoryObserved  MemoryLifecycle = "observed"
	MemoryCandidate MemoryLifecycle = "candidate"
	MemoryVerified  MemoryLifecycle = "verified"
	MemoryDurable   MemoryLifecycle = "durable"
	// Exceptional paths
	MemoryRejected   MemoryLifecycle = "rejected"
	MemoryStale      MemoryLifecycle = "stale"
	MemoryConflicted MemoryLifecycle = "conflicted"
	MemorySuperseded MemoryLifecycle = "superseded"
	MemoryTombstoned MemoryLifecycle = "tombstoned"
)

func (l MemoryLifecycle) IsValid() bool {
	switch l {
	case MemoryObserved, MemoryCandidate, MemoryVerified, MemoryDurable,
		MemoryRejected, MemoryStale, MemoryConflicted, MemorySuperseded, MemoryTombstoned:
		return true
	}
	return false
}

// MemoryConfidence reflects how the content was established.
type MemoryConfidence string

const (
	ConfidenceVerified   MemoryConfidence = "verified"
	ConfidenceObserved   MemoryConfidence = "observed"
	ConfidenceInferred   MemoryConfidence = "inferred"
	ConfidenceUnverified MemoryConfidence = "unverified"
)

func (c MemoryConfidence) IsValid() bool {
	switch c {
	case ConfidenceVerified, ConfidenceObserved, ConfidenceInferred, ConfidenceUnverified:
		return true
	}
	return false
}

// MemoryAuthority classifies who may promote this record.
// Higher authority records require stronger governance.
type MemoryAuthority string

const (
	// AuthorityOperator: only a human operator or explicit policy may write.
	AuthorityOperator MemoryAuthority = "operator"
	// AuthorityPolicy: accepted by an explicit policy evaluation.
	AuthorityPolicy MemoryAuthority = "policy"
	// AuthorityVerified: multi-agent or evidence-backed consensus.
	AuthorityVerified MemoryAuthority = "verified"
	// AuthorityAgent: a single agent assertion (weakest).
	AuthorityAgent MemoryAuthority = "agent"
)

func (a MemoryAuthority) IsValid() bool {
	switch a {
	case AuthorityOperator, AuthorityPolicy, AuthorityVerified, AuthorityAgent:
		return true
	}
	return false
}

// MemorySource describes the structured origin of a record.
type MemorySource struct {
	Kind      string `json:"kind"`      // "repository" | "user" | "runtime" | "agent_handoff" | "test" | "external"
	Reference string `json:"reference"` // e.g. commit hash, task ID, agent ID, URL
	AgentID   string `json:"agent_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
}

// MemoryRecordV2 is the canonical durable memory record for MARSHAL.
// It supersedes the legacy MemoryRecord (policy.go) and the row schemas in
// persistent_agent_memory / decision_records / failure_memory_records.
//
// Derived index metadata (vector store IDs, FTS row IDs, graph node IDs) must
// never be stored in this struct — they belong in projection/index tables.
type MemoryRecordV2 struct {
	// --- Identity ---
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`

	// --- Classification ---
	Kind      MemoryKind      `json:"kind"`
	Lifecycle MemoryLifecycle `json:"lifecycle"`
	Confidence MemoryConfidence `json:"confidence,omitempty"`
	Authority MemoryAuthority `json:"authority"`

	// --- Content ---
	Title         string `json:"title"`
	Body          string `json:"body"`
	ContentDigest string `json:"content_digest,omitempty"` // SHA-256 of canonical content; set by store

	// --- Scope ---
	// Scope identifies the namespace: "project" | "task" | "agent" | "session" | "branch" | "team"
	Scope   string `json:"scope"`
	ScopeID string `json:"scope_id"`

	// --- Provenance / Source ---
	Source     MemorySource   `json:"source"`
	EvidenceIDs []string      `json:"evidence_ids,omitempty"` // artifact / attestation IDs
	HeadCommit string         `json:"head_commit,omitempty"`
	BranchName string         `json:"branch_name,omitempty"`
	WorktreeID string         `json:"worktree_id,omitempty"`
	SessionID  string         `json:"session_id,omitempty"`
	RunID      string         `json:"run_id,omitempty"`

	// --- Temporal model ---
	// ObservedAt: when the fact was observed in the real world.
	ObservedAt time.Time  `json:"observed_at"`
	// IngestedAt: when MARSHAL first recorded it.
	IngestedAt time.Time  `json:"ingested_at"`
	// ValidFrom / ValidTo: closed interval during which the fact is asserted true.
	ValidFrom  time.Time  `json:"valid_from"`
	ValidTo    *time.Time `json:"valid_to,omitempty"`
	// LastVerifiedAt: when the record was last independently confirmed.
	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`

	// --- Versioning ---
	Revision int64 `json:"revision"`

	// --- Supersession / conflict ---
	SupersededBy []string `json:"superseded_by,omitempty"` // IDs of newer records
	SupersedesID []string `json:"supersedes,omitempty"`    // IDs of older records this replaces
	ConflictIDs  []string `json:"conflict_ids,omitempty"`  // IDs of records in conflict

	// --- Access control ---
	// ACLScope restricts which agents/roles may read this record.
	// Empty means project-wide readable.
	ACLScope string `json:"acl_scope,omitempty"`

	// --- Record timestamps ---
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// --- Extension metadata (versioned, free-form, not canonical content) ---
	// ExtMeta is the ONLY field that may use map[string]any for canonical records.
	// It is excluded from the canonical digest.
	ExtMeta map[string]any `json:"ext_meta,omitempty"`
}

// Validate returns an error if the record is missing required fields or has
// invalid enum values. This is the validation boundary for canonical writes.
func (r *MemoryRecordV2) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("%w: memory record ID cannot be empty", ErrInvalid)
	}
	if r.ProjectID == "" {
		return fmt.Errorf("%w: memory record ProjectID cannot be empty", ErrInvalid)
	}
	if !r.Kind.IsValid() {
		return fmt.Errorf("%w: memory kind %q is not valid", ErrInvalid, r.Kind)
	}
	if !r.Lifecycle.IsValid() {
		return fmt.Errorf("%w: memory lifecycle %q is not valid", ErrInvalid, r.Lifecycle)
	}
	if r.Authority != "" && !r.Authority.IsValid() {
		return fmt.Errorf("%w: memory authority %q is not valid", ErrInvalid, r.Authority)
	}
	if strings.TrimSpace(r.Body) == "" {
		return fmt.Errorf("%w: memory record Body cannot be empty", ErrInvalid)
	}
	if r.Scope == "" {
		return fmt.Errorf("%w: memory record Scope cannot be empty", ErrInvalid)
	}
	if r.ScopeID == "" {
		return fmt.Errorf("%w: memory record ScopeID cannot be empty", ErrInvalid)
	}
	if r.ObservedAt.IsZero() {
		return fmt.Errorf("%w: memory record ObservedAt cannot be zero", ErrInvalid)
	}
	if r.ValidFrom.IsZero() {
		return fmt.Errorf("%w: memory record ValidFrom cannot be zero", ErrInvalid)
	}
	if r.ValidTo != nil && !r.ValidTo.IsZero() && r.ValidFrom.After(*r.ValidTo) {
		return fmt.Errorf("%w: ValidFrom %s is after ValidTo %s",
			ErrInvalid, r.ValidFrom.Format(time.RFC3339), r.ValidTo.Format(time.RFC3339))
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: memory record CreatedAt cannot be zero", ErrInvalid)
	}
	return nil
}

// CanonicalDigest returns the SHA-256 hex digest of the canonical content
// fields. It deliberately excludes mutable metadata: Revision, UpdatedAt,
// LastVerifiedAt, ExtMeta, SupersededBy, ConflictIDs, ACLScope, and
// ContentDigest itself. This makes the digest stable for content-addressed
// deduplication while permitting lifecycle mutations.
func (r *MemoryRecordV2) CanonicalDigest() string {
	h := sha256.New()

	// Sorted EvidenceIDs for stability.
	evids := make([]string, len(r.EvidenceIDs))
	copy(evids, r.EvidenceIDs)
	sort.Strings(evids)

	supersedes := make([]string, len(r.SupersedesID))
	copy(supersedes, r.SupersedesID)
	sort.Strings(supersedes)

	parts := []string{
		r.ID,
		r.ProjectID,
		string(r.Kind),
		string(r.Lifecycle),
		string(r.Authority),
		r.Title,
		r.Body,
		r.Scope,
		r.ScopeID,
		r.Source.Kind,
		r.Source.Reference,
		r.Source.AgentID,
		r.SessionID,
		r.RunID,
		r.HeadCommit,
		r.BranchName,
		r.ObservedAt.UTC().Format(time.RFC3339Nano),
		r.ValidFrom.UTC().Format(time.RFC3339Nano),
		strings.Join(evids, ","),
		strings.Join(supersedes, ","),
		r.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if r.ValidTo != nil {
		parts = append(parts, r.ValidTo.UTC().Format(time.RFC3339Nano))
	}

	for _, p := range parts {
		fmt.Fprint(h, p, "\x00")
	}
	return hex.EncodeToString(h.Sum(nil))
}
