package learning

import (
	"fmt"
	"strings"
	"time"
)

// ReplayClass describes how faithfully a recorded run can be reproduced.
// A run is not replayable just because its inputs were recorded; external
// dependencies and unrecorded environment make the weaker classes honest.
type ReplayClass string

const (
	ReplayExact         ReplayClass = "EXACT"
	ReplayBestEffort    ReplayClass = "BEST_EFFORT"
	ReplayNonReplayable ReplayClass = "NON_REPLAYABLE"
)

func validReplayClass(c ReplayClass) bool {
	switch c {
	case ReplayExact, ReplayBestEffort, ReplayNonReplayable:
		return true
	}
	return false
}

// ReplayRecord indexes one reproducible run. Replay is a description of what
// can be re-executed, never an instruction to re-execute: SideEffects records
// the external actions a replay would repeat, so an unsafe replay is refused
// rather than silently performed.
type ReplayRecord struct {
	ID           string       `json:"replay_id"`
	Binding      Entry        `json:"binding"`
	Class        ReplayClass  `json:"replay_class"`
	ToolVersions []Dependency `json:"tool_versions,omitempty"`
	Seeds        []string     `json:"seeds,omitempty"`
	ExternalDeps []string     `json:"external_dependencies,omitempty"`
	SideEffects  []string     `json:"external_side_effects,omitempty"`
	RecordedAt   time.Time
}

// ValidateReplay reports whether a replay record is honest about what it can
// reproduce. A record claiming EXACT while depending on unpinned external
// systems is rejected rather than downgraded silently.
func ValidateReplay(r ReplayRecord) error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("%w: replay id", ErrInvalid)
	}
	if !r.Binding.Valid() {
		return fmt.Errorf("%w: replay binding", ErrInvalid)
	}
	if !validReplayClass(r.Class) {
		return fmt.Errorf("%w: replay class", ErrInvalid)
	}
	if r.Class == ReplayExact && len(r.ExternalDeps) > 0 {
		return fmt.Errorf("%w: exact replay depends on external systems", ErrInvalid)
	}
	for _, s := range r.Seeds {
		if CarriesSecret(s) {
			return ErrSecretMaterial
		}
	}
	return nil
}

// Replayable reports whether the record may be replayed automatically.
// A replay that would repeat an external side effect is never automatic:
// Process 07 indexes reproducibility, it does not re-fire real-world actions.
func (r ReplayRecord) Replayable() bool {
	return r.Class != ReplayNonReplayable && len(r.SideEffects) == 0
}

// BenchmarkRecord is one reproducible evaluation result. Every measured field
// is a pointer or a Status so that "not measured" never renders as zero, and a
// benchmark result alone can never promote a provider.
type BenchmarkRecord struct {
	ID             string `json:"record_id"`
	Benchmark      string `json:"benchmark"`
	Version        string `json:"benchmark_version"`
	TaskID         string `json:"task_id"`
	TreeDigest     string `json:"tree_digest"`
	Mode           string `json:"mode"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	Resolved       Status `json:"resolved"`
	VerifierResult Status `json:"verifier_result"`
	Tokens         *int64 `json:"tokens,omitempty"`
	CostMicros     *int64 `json:"cost_micros,omitempty"`
	WallMillis     *int64 `json:"wall_millis,omitempty"`
	ModelCalls     *int64 `json:"model_calls,omitempty"`
	Escalations    int    `json:"escalations"`
	FailureReason  string `json:"failure_reason,omitempty"`
	RecordedAt     time.Time
}

// ValidateBenchmark requires a pinned benchmark version and an exact tree, so
// a score cannot float free of the code that produced it.
func ValidateBenchmark(b BenchmarkRecord) error {
	if strings.TrimSpace(b.ID) == "" {
		return fmt.Errorf("%w: record id", ErrInvalid)
	}
	if strings.TrimSpace(b.Benchmark) == "" || strings.TrimSpace(b.Version) == "" {
		return fmt.Errorf("%w: benchmark must be pinned to a version", ErrInvalid)
	}
	if strings.TrimSpace(b.TaskID) == "" {
		return fmt.Errorf("%w: task id", ErrInvalid)
	}
	if strings.TrimSpace(b.TreeDigest) == "" {
		return fmt.Errorf("%w: benchmark must bind an exact tree", ErrInvalid)
	}
	if !validStatus(b.Resolved) || !validStatus(b.VerifierResult) {
		return fmt.Errorf("%w: benchmark status", ErrInvalid)
	}
	return nil
}

// Baseline is a bounded regression baseline bound to an exact tree and
// environment. Divergence from a baseline triggers revalidation; only an
// invariant baseline turns divergence into a failure by itself.
type Baseline struct {
	ID      string `json:"baseline_id"`
	Kind    string `json:"kind"`
	Binding Entry  `json:"binding"`
	Metric  string `json:"metric"`
	Value   string `json:"value"`
	// Invariant marks a baseline whose violation is a failure rather than a
	// prompt to revalidate.
	Invariant  bool `json:"invariant"`
	RecordedAt time.Time
}

// BaselineOutcome is the result of comparing an observation to a baseline.
type BaselineOutcome string

const (
	BaselineMatch      BaselineOutcome = "MATCH"
	BaselineRevalidate BaselineOutcome = "REVALIDATE"
	BaselineViolation  BaselineOutcome = "VIOLATION"
	BaselineUnbound    BaselineOutcome = "UNBOUND"
)

// CompareBaseline reports what a divergence means. A baseline recorded against
// a different tree or environment cannot judge the current one: it returns
// UNBOUND rather than a false verdict.
func CompareBaseline(b Baseline, observed string, tree, environment string) BaselineOutcome {
	if b.Binding.TreeDigest != tree || b.Binding.EnvironmentDigest != environment {
		return BaselineUnbound
	}
	if b.Value == observed {
		return BaselineMatch
	}
	if b.Invariant {
		return BaselineViolation
	}
	return BaselineRevalidate
}

// OracleRecord is what one verifier actually caught and missed. It exists so a
// verifier's authority reflects measured detection, not reputation.
type OracleRecord struct {
	Verifier        string   `json:"verifier"`
	Version         string   `json:"verifier_version"`
	MutationsCaught []string `json:"mutations_caught,omitempty"`
	MutationsMissed []string `json:"mutations_missed,omitempty"`
	FalsePositives  int      `json:"false_positives"`
	FalseNegatives  int      `json:"false_negatives"`
	Flaky           bool     `json:"flaky"`
	ClusterID       string   `json:"cluster_id"`
	Observed        time.Time
}

// SoleOracleAllowed reports whether this verifier may stand alone for a
// critical claim. A verifier that misses mutations, reports false negatives or
// is flaky must be corroborated: a weak oracle is never the only oracle.
func SoleOracleAllowed(o OracleRecord) bool {
	if o.Flaky || o.FalseNegatives > 0 || len(o.MutationsMissed) > 0 {
		return false
	}
	return len(o.MutationsCaught) > 0
}

// Capability is one observed harness or provider capability. An observation is
// what MARSHAL actually saw; Documented is what the vendor claims. They are
// stored separately because they disagree often and the observation wins.
type Capability struct {
	Harness    string        `json:"harness"`
	Version    string        `json:"harness_version"`
	Feature    string        `json:"feature"`
	Observed   Status        `json:"observed"`
	Documented Status        `json:"documented"`
	Scope      Scope         `json:"scope"`
	ProjectID  string        `json:"project_id,omitempty"`
	Evidence   []EvidenceRef `json:"evidence,omitempty"`
	ExpiresAt  *time.Time    `json:"expires_at,omitempty"`
	RecordedAt time.Time
}

// ValidateCapability requires an exact harness version and real evidence.
// A capability inferred from documentation alone is not an observation.
func ValidateCapability(c Capability) error {
	if strings.TrimSpace(c.Harness) == "" || strings.TrimSpace(c.Version) == "" {
		return fmt.Errorf("%w: capability needs an exact harness version", ErrInvalid)
	}
	if strings.TrimSpace(c.Feature) == "" {
		return fmt.Errorf("%w: capability feature", ErrInvalid)
	}
	if !validStatus(c.Observed) || !validStatus(c.Documented) {
		return fmt.Errorf("%w: capability status", ErrInvalid)
	}
	if c.Observed != StatusNotRun && c.Observed != StatusUnknown && len(c.Evidence) == 0 {
		return fmt.Errorf("%w: observed capability without evidence", ErrNotPromotable)
	}
	if c.Scope == ScopeGeneral && c.ProjectID != "" {
		return fmt.Errorf("%w: general capability bound to a project", ErrInvalid)
	}
	return nil
}
