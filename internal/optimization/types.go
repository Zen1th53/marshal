// Package optimization implements MARSHAL Process 08, which turns verified
// Process 07 learning into bounded, evidence-backed optimization.
//
// Optimize outcomes, never truth. Learning may propose change; evidence must
// justify change; governance decides whether change is allowed. Nothing here
// may weaken hard governance, and a higher benchmark score never outranks a
// hard veto.
package optimization

import (
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

var (
	// ErrInvalid marks a malformed or unbound record.
	ErrInvalid = errors.New("optimization: invalid")
	// ErrBindingMismatch marks a record that does not describe the exact state
	// it is being applied to.
	ErrBindingMismatch = errors.New("optimization: binding mismatch")
	// ErrTampered marks a record whose content no longer matches its digest.
	ErrTampered = errors.New("optimization: tampered")
	// ErrVeto marks a candidate refused by governance. It is terminal: no
	// amount of additional benchmark evidence overturns it.
	ErrVeto = errors.New("optimization: governance veto")
	// ErrNotPromotable marks a candidate that failed a promotion gate.
	ErrNotPromotable = errors.New("optimization: not promotable")
	// ErrUnsafeCounterfactual marks a counterfactual that would repeat a
	// destructive or irreversible external action.
	ErrUnsafeCounterfactual = errors.New("optimization: unsafe counterfactual")
	// ErrStale marks evidence too old to authorize a promotion.
	ErrStale = errors.New("optimization: stale")
	// ErrNotFound marks a record that does not exist.
	ErrNotFound = errors.New("optimization: not found")
	// ErrConflict marks a compare-and-swap failure.
	ErrConflict = errors.New("optimization: conflict")
)

// Status is the Process 08 acceptance vocabulary. No other values exist.
type Status string

const (
	StatusPass    Status = "PASS"
	StatusFail    Status = "FAIL"
	StatusBlocked Status = "BLOCKED"
	StatusNotRun  Status = "NOT_RUN"
	StatusUnknown Status = "UNKNOWN"
)

// ValidStatus reports whether s is one of the five permitted values.
func ValidStatus(s Status) bool {
	switch s {
	case StatusPass, StatusFail, StatusBlocked, StatusNotRun, StatusUnknown:
		return true
	}
	return false
}

// Entry is the exact Process 07 binding an optimization cycle derives from.
//
// Optimization reasons about what learning established. Without a complete
// binding to a specific memory commit at a specific tree, the optimization
// paths that depend on it are blocked rather than run against a guess.
type Entry struct {
	ProjectID       string `json:"project_id"`
	MemoryCommitID  string `json:"memory_commit_id"`
	MemoryVersion   int64  `json:"memory_commit_version"`
	MemoryDigest    string `json:"memory_commit_digest"`
	SourceSHA       string `json:"source_sha"`
	TreeDigest      string `json:"tree_digest"`
	EnvironmentHash string `json:"environment_digest"`
	// Outcome is the Process 06 outcome the memory commit itself rests on.
	Outcome learning.Outcome `json:"outcome"`
	// Limitations carries forward what the source run did not establish.
	Limitations []string `json:"limitations,omitempty"`
}

// Valid reports whether the entry binds to one exact Process 07 state.
func (e Entry) Valid() bool {
	if e.ProjectID == "" || e.MemoryCommitID == "" || e.MemoryVersion <= 0 {
		return false
	}
	if e.MemoryDigest == "" || e.SourceSHA == "" || e.TreeDigest == "" || e.EnvironmentHash == "" {
		return false
	}
	switch e.Outcome {
	case learning.OutcomeVerifiedComplete, learning.OutcomePartial,
		learning.OutcomeFailed, learning.OutcomeBlocked:
		return true
	}
	return false
}

// Dimension names what part of the system a candidate proposes to change.
type Dimension string

const (
	DimRouting      Dimension = "ROUTING"
	DimCascade      Dimension = "CASCADE"
	DimVerifier     Dimension = "VERIFIER"
	DimHarness      Dimension = "HARNESS"
	DimNativeEffort Dimension = "NATIVE_EFFORT"
	DimContext      Dimension = "CONTEXT"
	DimFallback     Dimension = "FALLBACK"
	DimRetry        Dimension = "RETRY"
	DimBudget       Dimension = "BUDGET"
	DimTools        Dimension = "TOOLS"
	DimPlaybook     Dimension = "PLAYBOOK"
	DimConcurrency  Dimension = "CONCURRENCY"
)

// ValidDimension reports whether d is a recognized optimization dimension.
func ValidDimension(d Dimension) bool {
	switch d {
	case DimRouting, DimCascade, DimVerifier, DimHarness, DimNativeEffort,
		DimContext, DimFallback, DimRetry, DimBudget, DimTools,
		DimPlaybook, DimConcurrency:
		return true
	}
	return false
}

// Metric is one measured value with its provenance.
//
// Value is a pointer so an unmeasured metric is nil rather than zero. A cost
// nobody measured is not a cost of zero, and reporting it as one is how a
// candidate wins on a number that was never observed.
type Metric struct {
	Name string `json:"name"`
	// Value is nil when the metric was not measured.
	Value *float64 `json:"value,omitempty"`
	Unit  string   `json:"unit,omitempty"`
	// Source names where the number came from: which provider reported it,
	// or which external clock measured it.
	Source string `json:"source"`
	// Method records how it was sampled, so two numbers are only compared
	// when they were produced the same way.
	Method string `json:"method,omitempty"`
}

// Measured reports whether the metric carries an actual observation.
func (m Metric) Measured() bool { return m.Value != nil }

// Objective is one thing optimization is trying to improve, and the direction
// that counts as improvement.
type Objective struct {
	Name string `json:"name"`
	// HigherIsBetter distinguishes a success rate from a cost.
	HigherIsBetter bool `json:"higher_is_better"`
	// Weight orders objectives against each other. It never lets an objective
	// outrank a hard constraint.
	Weight float64 `json:"weight"`
}

// Constraint is a hard requirement. Constraints are not objectives with large
// weights: no objective score can satisfy a violated constraint.
type Constraint struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// Kind groups constraints so a veto can say which class was violated.
	Kind string `json:"kind"`
}

// Candidate is one proposed optimization, stated so it can be refused.
//
// Every field a reviewer needs to reject it is required: what it changes, why,
// on what evidence, what it risks and how it is undone.
type Candidate struct {
	ID         string    `json:"candidate_id"`
	Dimension  Dimension `json:"dimension"`
	Hypothesis string    `json:"hypothesis"`
	TaskScope  []string  `json:"task_scope"`
	// Evidence is the Process 07 basis for believing this might help.
	Evidence []learning.EvidenceRef `json:"evidence,omitempty"`
	Risks    []string               `json:"risks,omitempty"`
	// RollbackPlan is mandatory. A change that cannot be undone is not a
	// candidate for a bounded experiment.
	RollbackPlan string `json:"rollback_plan"`
	// VerificationPlan states how the change will be shown not to regress.
	VerificationPlan string `json:"verification_plan"`
	// SecurityImpact is stated by the proposer and checked by the veto.
	SecurityImpact string `json:"security_impact,omitempty"`
	// Effects declare what the candidate touches. The veto reads these rather
	// than trusting the hypothesis text.
	Effects Effects `json:"effects"`
	// ClusterID groups semantically equivalent candidates so the same idea
	// proposed five ways counts once.
	ClusterID  string `json:"cluster_id,omitempty"`
	Provenance string `json:"provenance"`
	CreatedAt  time.Time
}

// Effects declares what a candidate actually does. These are the fields the
// governance veto reads; a candidate cannot talk its way past them with a
// persuasive hypothesis.
type Effects struct {
	// WeakensApprovals, WeakensSandbox, WeakensNetwork and WeakensEvidence
	// each name a hard boundary the candidate would relax.
	WeakensApprovals bool `json:"weakens_approvals"`
	WeakensSandbox   bool `json:"weakens_sandbox"`
	WeakensNetwork   bool `json:"weakens_network"`
	WeakensEvidence  bool `json:"weakens_evidence"`
	// RemovesMandatoryVerification marks a candidate that would drop a check
	// a critical claim depends on.
	RemovesMandatoryVerification bool `json:"removes_mandatory_verification"`
	// HidesUnknown marks a candidate that would render UNKNOWN or NOT_RUN as
	// something more definite.
	HidesUnknown bool `json:"hides_unknown"`
	// ExpandsPermissions marks a candidate that widens what the runtime may do.
	ExpandsPermissions bool `json:"expands_permissions"`
	// ExposesSecrets marks a candidate that would send secret material
	// somewhere it is not already permitted.
	ExposesSecrets bool `json:"exposes_secrets"`
	// RequiresUngovernableProvider marks a candidate that depends on a
	// provider MARSHAL cannot govern.
	RequiresUngovernableProvider bool `json:"requires_ungovernable_provider"`
	// UncontrolledExternalEffects marks a candidate that would act outside
	// the sandbox in a way that is not bounded.
	UncontrolledExternalEffects bool `json:"uncontrolled_external_effects"`
	// ModifiesGovernance marks a candidate that would edit hard policy. Such a
	// change must loop back through Process 03 rather than be enacted here.
	ModifiesGovernance bool `json:"modifies_governance"`
	// EnablesFleetControl marks an effect that Community must reject. Enterprise
	// fleet execution is not implemented in this module.
	EnablesFleetControl bool `json:"enables_fleet_control"`
	// SpendsVerificationReserve marks a candidate that would move budget
	// reserved for verification into generation.
	SpendsVerificationReserve bool `json:"spends_verification_reserve"`
	// ViolatesGoalConstraint marks a candidate that contradicts the Goal or
	// the constitution.
	ViolatesGoalConstraint bool `json:"violates_goal_constraint"`
}

// Baseline pins everything an experiment is compared against.
//
// Without a pinned baseline there is no improvement claim to make: "faster
// than before" is meaningless when "before" is not a specific configuration
// at a specific tree.
type Baseline struct {
	ID               string            `json:"baseline_id"`
	MarshalSHA       string            `json:"marshal_sha"`
	MemoryCommitID   string            `json:"memory_commit_id"`
	RoutingConfig    string            `json:"routing_config_digest"`
	ModelVersions    map[string]string `json:"model_versions"`
	HarnessVersions  map[string]string `json:"harness_versions"`
	BenchmarkVersion string            `json:"benchmark_version,omitempty"`
	VerifierPolicy   string            `json:"verifier_policy_digest"`
	Toolchain        string            `json:"toolchain_digest"`
	EnvironmentHash  string            `json:"environment_digest"`
	BudgetMicros     *int64            `json:"budget_micros,omitempty"`
	Seeds            []string          `json:"seeds,omitempty"`
	RecordedAt       time.Time
}

// Comparable reports whether two baselines describe configurations that can
// honestly be compared. Anything that differs beyond the candidate's own
// change makes the comparison meaningless.
func (b Baseline) Comparable(other Baseline) bool {
	if b.MarshalSHA != other.MarshalSHA || b.EnvironmentHash != other.EnvironmentHash {
		return false
	}
	if b.BenchmarkVersion != other.BenchmarkVersion || b.Toolchain != other.Toolchain {
		return false
	}
	if len(b.ModelVersions) != len(other.ModelVersions) {
		return false
	}
	for k, v := range b.ModelVersions {
		if other.ModelVersions[k] != v {
			return false
		}
	}
	if len(b.HarnessVersions) != len(other.HarnessVersions) {
		return false
	}
	for k, v := range b.HarnessVersions {
		if other.HarnessVersions[k] != v {
			return false
		}
	}
	return true
}

// Valid reports whether the baseline pins enough to authorize a comparison.
func (b Baseline) Valid() bool {
	return b.ID != "" && b.MarshalSHA != "" && b.EnvironmentHash != "" &&
		b.RoutingConfig != "" && b.VerifierPolicy != "" && b.Toolchain != ""
}

// Decision is the outcome of a promotion evaluation.
type Decision string

const (
	// DecisionPromote allows the change within the evidence's scope.
	DecisionPromote Decision = "PROMOTE"
	// DecisionPromoteBounded allows it only for the task classes the evidence
	// actually covers.
	DecisionPromoteBounded Decision = "PROMOTE_BOUNDED"
	DecisionReject         Decision = "REJECT"
	DecisionNeedsEvidence  Decision = "NEEDS_MORE_EVIDENCE"
	DecisionBlocked        Decision = "BLOCKED"
	// DecisionStale marks evidence that has aged out of relevance.
	DecisionStale Decision = "STALE"
)

// Cycle is the canonical, versioned Process 08 record.
type Cycle struct {
	ID          string       `json:"optimization_id"`
	Binding     Entry        `json:"binding"`
	Objectives  []Objective  `json:"objectives"`
	Constraints []Constraint `json:"constraints"`
	Candidates  []Candidate  `json:"candidates,omitempty"`
	Baselines   []Baseline   `json:"baselines,omitempty"`
	// ExperimentRefs point at counterfactual, benchmark and canary records.
	ExperimentRefs []string `json:"experiment_refs,omitempty"`
	// Decisions records every promotion evaluation, including the refusals.
	Decisions []PromotionRecord `json:"decisions,omitempty"`
	// Vetoes records governance refusals so a rejected candidate leaves a
	// trail rather than vanishing.
	Vetoes     []VetoRecord `json:"vetoes,omitempty"`
	Provenance string       `json:"provenance"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Version    int64  `json:"version"`
	Digest     string `json:"digest"`
	// BlockedOptimization records paths that could not run and why.
	BlockedOptimization []string `json:"blocked_optimization,omitempty"`
}

// VetoRecord is one governance refusal, kept so the reason survives.
type VetoRecord struct {
	CandidateID string    `json:"candidate_id"`
	Reasons     []string  `json:"reasons"`
	DecidedAt   time.Time `json:"decided_at"`
}

// PromotionRecord is one promotion evaluation and everything it rested on.
type PromotionRecord struct {
	CandidateID string   `json:"candidate_id"`
	BaselineID  string   `json:"baseline_id"`
	Decision    Decision `json:"decision"`
	Reasons     []string `json:"reasons,omitempty"`
	// Scope bounds where a PROMOTE_BOUNDED decision applies.
	Scope []string `json:"scope,omitempty"`
	// EvidenceRefs point at the experiments that justified the decision.
	EvidenceRefs []string  `json:"evidence_refs,omitempty"`
	DecidedAt    time.Time `json:"decided_at"`
	// Digest binds the decision to the exact inputs that produced it.
	Digest string `json:"digest"`
}
