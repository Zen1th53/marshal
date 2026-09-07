package constitution

import (
	"fmt"
	"sort"
	"strings"
)

// InvariantID is the stable machine address of a constitutional rule. IDs are
// part of the contract: audit records, reason codes and regression tests refer
// to them, so they are never renumbered within a major version.
type InvariantID string

const (
	// Article I / XV — AI is a reasoning component, never an authority.
	InvAIMayNotAuthorize InvariantID = "CI-001-AI-MAY-NOT-AUTHORIZE"
	// Article XV — approvals bind to an exact action and cannot be replayed.
	InvApprovalIntegrity InvariantID = "CI-002-APPROVAL-INTEGRITY"
	// Article XV / authz — no principal approves their own request.
	InvNoSelfApproval InvariantID = "CI-003-NO-SELF-APPROVAL"
	// Article II — lower authority never overrides higher authority.
	InvAuthorityPrecedence InvariantID = "CI-004-AUTHORITY-PRECEDENCE"
	// Article X — isolation is fail-closed and never weakened to pass a test.
	InvSandboxFailClosed InvariantID = "CI-005-SANDBOX-FAIL-CLOSED"
	// Article X — egress crosses an endpoint-enforcing policy boundary.
	InvNetworkFailClosed InvariantID = "CI-006-NETWORK-FAIL-CLOSED"
	// Article VIII — scope does not expand silently.
	InvNoSilentScopeExpansion InvariantID = "CI-007-NO-SILENT-SCOPE-EXPANSION"
	// Article IV — claims are evidence-backed; assertion is not proof.
	InvEvidenceBackedClaims InvariantID = "CI-008-EVIDENCE-BACKED-CLAIMS"
	// Article IV — proof expires when the state it describes changes.
	InvEvidenceFreshness InvariantID = "CI-009-EVIDENCE-FRESHNESS"
	// Article XVI — AI proposes memory; policy promotes it.
	InvMemoryPromotionGoverned InvariantID = "CI-010-MEMORY-PROMOTION-GOVERNED"
	// Article XXI — project state never crosses into another project.
	InvProjectIsolation InvariantID = "CI-011-PROJECT-ISOLATION"
	// Article XVIII — VERIFIED is issued by completion policy alone.
	InvCompletionIsPolicy InvariantID = "CI-012-COMPLETION-IS-POLICY"
	// Article XI — a surface never claims state the backend has not reached.
	InvTruthfulControlSurface InvariantID = "CI-013-TRUTHFUL-CONTROL-SURFACE"
	// Article XXII — a session is bound to one constitution version.
	InvConstitutionVersionBinding InvariantID = "CI-014-CONSTITUTION-VERSION-BINDING"
	// Article XIII — native harness config never outranks MARSHAL.
	InvNativeConfigSubordinate InvariantID = "CI-015-NATIVE-CONFIG-SUBORDINATE"
	// Article III / XIX — work stays aligned to the accepted intent.
	InvGoalFidelity InvariantID = "CI-016-GOAL-FIDELITY"
	// Rollback is physical, verified restoration — never an audit record alone.
	InvRollbackTruthful InvariantID = "CI-017-ROLLBACK-TRUTHFUL"
	// ULTRA raises capability, never lowers a guarantee.
	InvUltraNoWeakening InvariantID = "CI-018-ULTRA-NO-WEAKENING"
	// Article IV — no invented confidence; UNKNOWN is a valid answer.
	InvNoInventedConfidence InvariantID = "CI-019-NO-INVENTED-CONFIDENCE"
	// Article XXIV — secrets and hidden reasoning never become canonical.
	InvSecretsNotCanonical InvariantID = "CI-020-SECRETS-NOT-CANONICAL"
)

// Severity determines how a violated invariant is handled. It is not advisory:
// the gate engine maps severity directly onto an outcome.
type Severity string

const (
	// SeverityHard is a security or truth boundary. A hard violation blocks and
	// can never be approved away, delegated, or overridden by ULTRA.
	SeverityHard Severity = "HARD"
	// SeverityApproval marks an action that is legitimate but requires explicit
	// human authorization bound to this exact action.
	SeverityApproval Severity = "APPROVAL"
	// SeverityDegrade marks a condition that permits reduced operation with a
	// truthful degraded status rather than a claim of full function.
	SeverityDegrade Severity = "DEGRADE"
)

// Invariant is the machine-addressable form of a constitutional rule.
type Invariant struct {
	ID InvariantID `json:"id"`
	// Article is the constitutional article the invariant enforces.
	Article string `json:"article"`
	// Severity fixes the gate outcome when the invariant is violated.
	Severity Severity `json:"severity"`
	// Reason is the stable machine code emitted on violation.
	Reason ReasonCode `json:"reason"`
	// Explanation is safe to show a user. It never contains internals,
	// secrets, provider output or hidden reasoning.
	Explanation string `json:"explanation"`
	// Domains lists the decision domains the invariant applies to. An empty
	// slice means the invariant applies to every domain.
	Domains []Domain `json:"domains,omitempty"`
}

// AppliesTo reports whether the invariant governs the given decision domain.
func (i Invariant) AppliesTo(domain Domain) bool {
	if len(i.Domains) == 0 {
		return true
	}
	for _, d := range i.Domains {
		if d == domain {
			return true
		}
	}
	return false
}

// Registry is the immutable set of invariants for one constitution version.
// It is built once at load time and is safe for concurrent readers.
type Registry struct {
	version    Version
	invariants map[InvariantID]Invariant
	order      []InvariantID
}

// Version reports the constitution version this registry implements.
func (r *Registry) Version() Version { return r.version }

// Lookup returns the invariant with the given ID.
func (r *Registry) Lookup(id InvariantID) (Invariant, bool) {
	if r == nil {
		return Invariant{}, false
	}
	inv, ok := r.invariants[id]
	return inv, ok
}

// All returns every invariant in stable ID order.
func (r *Registry) All() []Invariant {
	if r == nil {
		return nil
	}
	out := make([]Invariant, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.invariants[id])
	}
	return out
}

// ForDomain returns the invariants governing a decision domain, in stable order.
func (r *Registry) ForDomain(domain Domain) []Invariant {
	if r == nil {
		return nil
	}
	out := make([]Invariant, 0, len(r.order))
	for _, id := range r.order {
		if inv := r.invariants[id]; inv.AppliesTo(domain) {
			out = append(out, inv)
		}
	}
	return out
}

// Len reports the number of registered invariants.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.order)
}

// NewRegistry builds a validated registry. It rejects duplicate or malformed
// invariants so that a broken constitution fails at load rather than silently
// under-enforcing at a gate.
func NewRegistry(version Version, invariants []Invariant) (*Registry, error) {
	if version.IsZero() {
		return nil, fmt.Errorf("%w: constitution version is required", ErrInvalidConstitution)
	}
	if len(invariants) == 0 {
		return nil, fmt.Errorf("%w: constitution has no invariants", ErrInvalidConstitution)
	}
	registry := &Registry{version: version, invariants: make(map[InvariantID]Invariant, len(invariants))}
	for _, inv := range invariants {
		if strings.TrimSpace(string(inv.ID)) == "" {
			return nil, fmt.Errorf("%w: invariant has an empty ID", ErrInvalidConstitution)
		}
		if _, exists := registry.invariants[inv.ID]; exists {
			return nil, fmt.Errorf("%w: invariant %s is declared twice", ErrInvalidConstitution, inv.ID)
		}
		if err := inv.validate(); err != nil {
			return nil, err
		}
		registry.invariants[inv.ID] = inv
		registry.order = append(registry.order, inv.ID)
	}
	sort.Slice(registry.order, func(a, b int) bool { return registry.order[a] < registry.order[b] })
	return registry, nil
}

func (i Invariant) validate() error {
	switch i.Severity {
	case SeverityHard, SeverityApproval, SeverityDegrade:
	default:
		return fmt.Errorf("%w: invariant %s has invalid severity %q", ErrInvalidConstitution, i.ID, i.Severity)
	}
	if strings.TrimSpace(string(i.Reason)) == "" {
		return fmt.Errorf("%w: invariant %s has no reason code", ErrInvalidConstitution, i.ID)
	}
	if strings.TrimSpace(i.Explanation) == "" {
		return fmt.Errorf("%w: invariant %s has no user explanation", ErrInvalidConstitution, i.ID)
	}
	if strings.TrimSpace(i.Article) == "" {
		return fmt.Errorf("%w: invariant %s is not linked to an article", ErrInvalidConstitution, i.ID)
	}
	for _, domain := range i.Domains {
		if !domain.Valid() {
			return fmt.Errorf("%w: invariant %s references unknown domain %q", ErrInvalidConstitution, i.ID, domain)
		}
	}
	return nil
}

// Default returns the registry for the constitution version this build
// implements. It panics only if the built-in table is itself malformed, which
// is a programming error caught by the package tests.
func Default() *Registry {
	registry, err := NewRegistry(Current, builtinInvariants())
	if err != nil {
		panic("constitution: built-in registry is invalid: " + err.Error())
	}
	return registry
}

func builtinInvariants() []Invariant {
	return []Invariant{
		{
			ID: InvAIMayNotAuthorize, Article: "I", Severity: SeverityHard,
			Reason:      ReasonAIAuthorityRefused,
			Explanation: "An AI recommendation cannot authorize this action on its own.",
		},
		{
			ID: InvApprovalIntegrity, Article: "XV", Severity: SeverityHard,
			Reason:      ReasonApprovalStale,
			Explanation: "The approval on file does not match this exact action any more.",
			Domains:     []Domain{DomainApproval, DomainFileMutation, DomainShell, DomainNetwork, DomainExternalEffect, DomainCredential, DomainRollback},
		},
		{
			ID: InvNoSelfApproval, Article: "XV", Severity: SeverityHard,
			Reason:      ReasonSelfApproval,
			Explanation: "The requester of an action cannot also approve it.",
			Domains:     []Domain{DomainApproval, DomainCompletion, DomainVerification, DomainMemoryPromotion},
		},
		{
			ID: InvAuthorityPrecedence, Article: "II", Severity: SeverityHard,
			Reason:      ReasonAuthorityPrecedenceViolated,
			Explanation: "A lower-authority source tried to override a higher-authority rule.",
		},
		{
			ID: InvSandboxFailClosed, Article: "X", Severity: SeverityHard,
			Reason:      ReasonSandboxUnavailable,
			Explanation: "Required isolation is unavailable, so execution is blocked.",
			Domains:     []Domain{DomainShell, DomainFileMutation, DomainExternalEffect},
		},
		{
			ID: InvNetworkFailClosed, Article: "X", Severity: SeverityHard,
			Reason:      ReasonNetworkNotEnforced,
			Explanation: "Outbound access is blocked because policy enforcement is unavailable.",
			Domains:     []Domain{DomainNetwork, DomainExternalEffect},
		},
		{
			ID: InvNoSilentScopeExpansion, Article: "VIII", Severity: SeverityApproval,
			Reason:      ReasonScopeEscape,
			Explanation: "This action reaches outside the agreed scope of work.",
			Domains:     []Domain{DomainFileMutation, DomainShell, DomainProjectMutation, DomainGoalMutation, DomainPlanMutation, DomainExternalEffect},
		},
		{
			ID: InvEvidenceBackedClaims, Article: "IV", Severity: SeverityHard,
			Reason:      ReasonEvidenceInsufficient,
			Explanation: "This result is not backed by evidence, so it cannot be treated as proven.",
			Domains:     []Domain{DomainCompletion, DomainVerification, DomainMemoryPromotion, DomainRecommendation},
		},
		{
			ID: InvEvidenceFreshness, Article: "IV", Severity: SeverityHard,
			Reason:      ReasonEvidenceStale,
			Explanation: "The supporting evidence predates the current state and must be re-verified.",
			Domains:     []Domain{DomainCompletion, DomainVerification, DomainMemoryPromotion, DomainApproval},
		},
		{
			ID: InvMemoryPromotionGoverned, Article: "XVI", Severity: SeverityHard,
			Reason:      ReasonMemoryPromotionRefused,
			Explanation: "Long-term memory is written by MARSHAL policy, not directly by a model.",
			Domains:     []Domain{DomainMemoryPromotion},
		},
		{
			ID: InvProjectIsolation, Article: "XXI", Severity: SeverityHard,
			Reason:      ReasonCrossProjectLeak,
			Explanation: "State from another project cannot be used here.",
		},
		{
			ID: InvCompletionIsPolicy, Article: "XVIII", Severity: SeverityHard,
			Reason:      ReasonCompletionNotAuthorized,
			Explanation: "Work is marked verified by MARSHAL policy, not by a model's own judgement.",
			Domains:     []Domain{DomainCompletion},
		},
		{
			ID: InvTruthfulControlSurface, Article: "XI", Severity: SeverityHard,
			Reason:      ReasonFalseSuccess,
			Explanation: "The requested status is not supported by the recorded state.",
		},
		{
			ID: InvConstitutionVersionBinding, Article: "XXII", Severity: SeverityHard,
			Reason:      ReasonConstitutionVersionMismatch,
			Explanation: "This session follows a different constitution version than this runtime implements.",
		},
		{
			ID: InvNativeConfigSubordinate, Article: "XIII", Severity: SeverityHard,
			Reason:      ReasonNativeInstructionRefused,
			Explanation: "Instructions found in tool or project configuration cannot override MARSHAL rules.",
		},
		{
			ID: InvGoalFidelity, Article: "III", Severity: SeverityApproval,
			Reason:      ReasonGoalDrift,
			Explanation: "This step no longer matches the goal that was accepted.",
			Domains:     []Domain{DomainGoalMutation, DomainPlanMutation, DomainCompletion, DomainFileMutation},
		},
		{
			ID: InvRollbackTruthful, Article: "XIV", Severity: SeverityHard,
			Reason:      ReasonRollbackUnverified,
			Explanation: "Rollback is only reported as done once the restored state has been checked.",
			Domains:     []Domain{DomainRollback},
		},
		{
			ID: InvUltraNoWeakening, Article: "VI", Severity: SeverityHard,
			Reason:      ReasonUltraCannotWeaken,
			Explanation: "ULTRA adds capability but cannot lift a security or approval requirement.",
		},
		{
			ID: InvNoInventedConfidence, Article: "IV", Severity: SeverityHard,
			Reason:      ReasonInventedConfidence,
			Explanation: "Confidence figures must come from measured criteria, not from an estimate.",
			Domains:     []Domain{DomainCompletion, DomainVerification, DomainRecommendation},
		},
		{
			// Unscoped deliberately: credential material must be refused
			// wherever it appears, not only on the domains that persist data.
			// A secret riding along in a file write or a shell argument is the
			// same exposure as one written to memory.
			ID: InvSecretsNotCanonical, Article: "XXIV", Severity: SeverityHard,
			Reason:      ReasonSecretExposure,
			Explanation: "Credentials and hidden model reasoning are never stored as project knowledge.",
		},
	}
}
