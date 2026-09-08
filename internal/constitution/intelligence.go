package constitution

import (
	"strings"
	"time"
)

// This file defines the Control Intelligence contract: what MARSHAL tells a
// model about a decision, and what a model may say back.
//
// The contract is provider-neutral by construction. It is plain Go data with
// no dependency on any harness's prompt or tool-call semantics, so Codex,
// Claude-compatible harnesses, OpenCode and Antigravity all present the same
// shape and none of them can express something the others cannot.
//
// The asymmetry between the two halves is the point. Advisory input is rich
// because reasoning benefits from context. Advisory output is inert: it
// carries no field that any gate reads as permission. A model can say "this
// should be allowed" and that sentence has no more force than any other
// sentence it produces.

// Freshness qualifies how current a piece of supplied context is. STALE
// context is passed to the model labelled rather than silently dropped, so
// reasoning can account for it.
type Freshness string

const (
	FreshnessFresh   Freshness = "FRESH"
	FreshnessStale   Freshness = "STALE"
	FreshnessUnknown Freshness = "UNKNOWN"
)

// ContextItem is one piece of canonical context handed to the model, carrying
// its own provenance and freshness so the model is never asked to guess how
// much to trust it.
type ContextItem struct {
	Kind      string    `json:"kind"`
	Ref       string    `json:"ref"`
	Summary   string    `json:"summary"`
	Source    string    `json:"source"`
	Freshness Freshness `json:"freshness"`
	// ObservedAt is when the underlying fact was last confirmed.
	ObservedAt time.Time `json:"observed_at,omitempty"`
}

// GovernanceState reports how well MARSHAL can actually govern a harness. It
// is evidence-derived: VerifiedGoverned requires a successful effective-config
// verification, and the honest states are used whenever that is missing rather
// than assuming governance holds (task 31).
type GovernanceState string

const (
	GovernanceVerified    GovernanceState = "VERIFIED_GOVERNED"
	GovernanceDegraded    GovernanceState = "DEGRADED"
	GovernanceUnverified  GovernanceState = "UNVERIFIED"
	GovernanceUnavailable GovernanceState = "UNAVAILABLE"
)

// Governed reports whether MARSHAL has proven it controls the harness. Only
// the verified state qualifies; the rest are truthful admissions that it has
// not been proven, and callers must not treat them as success.
func (g GovernanceState) Governed() bool { return g == GovernanceVerified }

// Advisory is the structured recommendation a model returns.
//
// Every field is advice. None of it authorizes anything. The gate reads an
// Advisory for its assessment and its declared unknowns, and treats a missing
// or malformed Advisory as absent input rather than as a failure that blocks
// the decision, because a model that cannot answer must not be able to stall
// MARSHAL either.
type Advisory struct {
	// Interpretation is the model's reading of what is being asked.
	Interpretation string `json:"interpretation"`
	// Assumptions the model made to reach its recommendation.
	Assumptions []string `json:"assumptions,omitempty"`
	// Ambiguities and conflicts the model noticed but could not resolve.
	Ambiguities []string `json:"ambiguities,omitempty"`
	// ProposedAction is what the model suggests doing. It is a suggestion.
	ProposedAction string `json:"proposed_action,omitempty"`
	// AssessedRisk is the model's opinion of the risk level. MARSHAL's own
	// risk engine remains authoritative; a lower opinion here never lowers it.
	AssessedRisk string `json:"assessed_risk,omitempty"`
	// AssessedReversibility is likewise an opinion.
	AssessedReversibility Reversibility `json:"assessed_reversibility,omitempty"`
	// EvidenceRefs point at evidence the model consulted.
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	// Unknowns the model is explicitly declaring. Declaring an unknown is the
	// correct behaviour and is rewarded by the gate, not penalised.
	Unknowns []string `json:"unknowns,omitempty"`
	// RecommendsApproval asks MARSHAL to require human approval. A model may
	// always raise the bar; it can never lower it, so there is deliberately no
	// field by which a model can state that approval is unnecessary.
	RecommendsApproval bool `json:"recommends_approval,omitempty"`
	// RecommendsReviewer asks for independent review.
	RecommendsReviewer bool `json:"recommends_reviewer,omitempty"`
	// MemoryCandidates the model suggests remembering. They enter the
	// candidate pipeline and are never written directly (Article XVI).
	MemoryCandidates []string `json:"memory_candidates,omitempty"`
	// SelfCheck is the model's structured constitutional self-assessment.
	SelfCheck SelfCheck `json:"self_check"`
}

// SelfCheck is the model's own account of how its proposal sits against the
// constitution. It exists to surface reasoning MARSHAL can cross-examine, not
// to let a model certify itself: a self-check claiming everything is fine has
// no effect on any gate outcome, while a self-check admitting a problem is
// taken seriously.
type SelfCheck struct {
	// AlignedWithGoal is the model's claim that its proposal serves the goal.
	AlignedWithGoal bool `json:"aligned_with_goal"`
	// RespectsConstraints is its claim about the hard constraints.
	RespectsConstraints bool `json:"respects_constraints"`
	// ExpandsScope admits that the proposal reaches beyond the agreed scope.
	ExpandsScope bool `json:"expands_scope"`
	// EvidenceSufficient is its claim that the evidence supports the proposal.
	EvidenceSufficient bool `json:"evidence_sufficient"`
	// DependsOnUnknowns admits reliance on something not established.
	DependsOnUnknowns bool `json:"depends_on_unknowns"`
	// DependsOnStaleContext admits reliance on context labelled STALE.
	DependsOnStaleContext bool `json:"depends_on_stale_context"`
	// IncreasesPrivilege admits the proposal needs more privilege than held.
	IncreasesPrivilege bool `json:"increases_privilege"`
	// Destructive admits the proposal destroys or overwrites state.
	Destructive bool `json:"destructive"`
	// RecoveryAvailable is its claim that the action can be undone.
	RecoveryAvailable bool `json:"recovery_available"`
	// ConstitutionVersion echoes the version the model was briefed on. A
	// mismatch means the model reasoned under different rules than the ones
	// in force, so its advice is discarded.
	ConstitutionVersion Version `json:"constitution_version"`
	// Notes carries free text for anything the structured fields cannot hold.
	Notes string `json:"notes,omitempty"`
}

// Admissions reports the self-check fields in which the model conceded a
// governance problem. The gate escalates on these; it never relaxes on the
// absence of them, since a model can always decline to admit anything.
func (s SelfCheck) Admissions() []string {
	var admissions []string
	if !s.AlignedWithGoal {
		admissions = append(admissions, "not aligned with the goal")
	}
	if !s.RespectsConstraints {
		admissions = append(admissions, "does not respect hard constraints")
	}
	if s.ExpandsScope {
		admissions = append(admissions, "expands scope")
	}
	if !s.EvidenceSufficient {
		admissions = append(admissions, "evidence is insufficient")
	}
	if s.DependsOnUnknowns {
		admissions = append(admissions, "depends on unknowns")
	}
	if s.DependsOnStaleContext {
		admissions = append(admissions, "depends on stale context")
	}
	if s.IncreasesPrivilege {
		admissions = append(admissions, "increases privilege")
	}
	if s.Destructive && !s.RecoveryAvailable {
		admissions = append(admissions, "is destructive with no recovery")
	}
	return admissions
}

// Brief is the provider-neutral input handed to the Control Intelligence.
//
// It carries what a model needs in order to reason well: the canonical
// identifiers, the original request, the constraints it must respect, the
// acceptance criteria it will be measured against, the current plan step, the
// relevant memory and evidence, and the honest state of the sandbox, network,
// harness governance and budget.
type Brief struct {
	Envelope Envelope `json:"envelope"`

	// OriginalRequest is the user's own words, preserved verbatim across the
	// session so that intent is never reconstructed from a paraphrase
	// (Article III).
	OriginalRequest string `json:"original_request"`
	// HardConstraints are the user-approved limits that outrank the plan.
	HardConstraints []string `json:"hard_constraints,omitempty"`
	// AcceptanceCriteria are what completion will actually be measured on.
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`

	// CurrentPlanStep describes where the work has reached.
	CurrentPlanStep string `json:"current_plan_step,omitempty"`
	// RelevantMemory, Evidence and Conflicts are supplied with provenance and
	// freshness attached.
	RelevantMemory []ContextItem `json:"relevant_memory,omitempty"`
	Evidence       []ContextItem `json:"evidence,omitempty"`
	Conflicts      []ContextItem `json:"conflicts,omitempty"`

	// HeldApprovals lists approvals already granted for this work.
	HeldApprovals []string `json:"held_approvals,omitempty"`

	// SandboxAvailable and NetworkEnforced report the true isolation state.
	// They are reported honestly even when false, because a model reasoning
	// about a sandbox that is not there gives worse advice, not better.
	SandboxAvailable bool `json:"sandbox_available"`
	NetworkEnforced  bool `json:"network_enforced"`
	// HarnessGovernance is the evidence-derived governance state.
	HarnessGovernance GovernanceState `json:"harness_governance"`

	// BudgetRemaining and Terminating describe the budget and stop state.
	BudgetRemaining string `json:"budget_remaining,omitempty"`
	Terminating     bool   `json:"terminating,omitempty"`

	// AllowedDomains bounds what the model may propose. A proposal outside
	// these domains is out of contract and is discarded.
	AllowedDomains []Domain `json:"allowed_domains,omitempty"`
}

// Validate checks that a brief carries the context required for the model to
// reason honestly. A brief missing the original request or the constitution
// version would invite the model to invent them.
func (b Brief) Validate() error {
	if err := b.Envelope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(b.OriginalRequest) == "" {
		return errInvalidf("brief for decision %s omits the original user request", b.Envelope.DecisionID)
	}
	switch b.HarnessGovernance {
	case GovernanceVerified, GovernanceDegraded, GovernanceUnverified, GovernanceUnavailable:
	default:
		return errInvalidf("brief for decision %s has unknown harness governance state %q", b.Envelope.DecisionID, b.HarnessGovernance)
	}
	for _, domain := range b.AllowedDomains {
		if !domain.Valid() {
			return errInvalidf("brief for decision %s allows unknown domain %q", b.Envelope.DecisionID, domain)
		}
	}
	return nil
}

// PermitsDomain reports whether the brief allows a proposal in the domain. An
// empty AllowedDomains list permits only the envelope's own domain, so an
// unset field cannot be read as unrestricted permission.
func (b Brief) PermitsDomain(domain Domain) bool {
	if len(b.AllowedDomains) == 0 {
		return domain == b.Envelope.Domain
	}
	for _, allowed := range b.AllowedDomains {
		if allowed == domain {
			return true
		}
	}
	return false
}

// AdvisoryStatus reports how an advisory was treated.
type AdvisoryStatus string

const (
	// AdvisoryAccepted means the advisory was well-formed and was read as
	// input. It does not mean the proposal was followed.
	AdvisoryAccepted AdvisoryStatus = "ACCEPTED"
	// AdvisoryDiscarded means the advisory was malformed, out of contract, or
	// reasoned under a different constitution version. The decision continues
	// without it.
	AdvisoryDiscarded AdvisoryStatus = "DISCARDED"
	// AdvisoryAbsent means no advisory was supplied.
	AdvisoryAbsent AdvisoryStatus = "ABSENT"
)

// ValidateAdvisory checks an advisory against the brief that produced it and
// reports how it should be treated.
//
// A malformed advisory is discarded rather than fatal. This is deliberate: a
// model that emits garbage, or that is compromised into emitting an
// instruction, must not be able to halt MARSHAL any more than it can command
// it. The decision proceeds on deterministic grounds either way.
func ValidateAdvisory(brief Brief, advisory *Advisory) (AdvisoryStatus, ReasonCode) {
	if advisory == nil {
		return AdvisoryAbsent, ReasonAllowed
	}
	// A model briefed under one constitution cannot advise under another.
	if advisory.SelfCheck.ConstitutionVersion.Compare(brief.Envelope.ConstitutionVersion) != 0 {
		return AdvisoryDiscarded, ReasonConstitutionVersionMismatch
	}
	if strings.TrimSpace(advisory.Interpretation) == "" {
		return AdvisoryDiscarded, ReasonAdvisoryMalformed
	}
	return AdvisoryAccepted, ReasonAllowed
}
