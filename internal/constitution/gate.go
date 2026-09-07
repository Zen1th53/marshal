package constitution

import (
	"sort"
	"strings"
	"time"
)

// Outcome is the constitutional verdict on a decision. It is produced only by
// Evaluate below; no other code path in MARSHAL mints one, and nothing a model
// returns can be converted into one.
type Outcome string

const (
	OutcomeAllow               Outcome = "ALLOW"
	OutcomeBlock               Outcome = "BLOCK"
	OutcomeRequireApproval     Outcome = "REQUIRE_APPROVAL"
	OutcomeDegrade             Outcome = "DEGRADE"
	OutcomeSuspend             Outcome = "SUSPEND"
	OutcomeReplan              Outcome = "REPLAN"
	OutcomeRequireVerification Outcome = "REQUIRE_VERIFICATION"
)

// Permits reports whether the outcome lets the action proceed now. Only ALLOW
// does. Every other outcome, including DEGRADE, requires something to change
// first, so callers cannot treat a non-block as a go-ahead.
func (o Outcome) Permits() bool { return o == OutcomeAllow }

// rank orders outcomes by how restrictive they are. When several invariants
// fire, the most restrictive wins: a decision is never softened because some
// other check happened to be satisfied.
func (o Outcome) rank() int {
	switch o {
	case OutcomeBlock:
		return 6
	case OutcomeSuspend:
		return 5
	case OutcomeRequireApproval:
		return 4
	case OutcomeReplan:
		return 3
	case OutcomeRequireVerification:
		return 2
	case OutcomeDegrade:
		return 1
	default:
		return 0
	}
}

// Finding records one invariant that fired during evaluation.
type Finding struct {
	Invariant InvariantID `json:"invariant"`
	Severity  Severity    `json:"severity"`
	Reason    ReasonCode  `json:"reason"`
	Outcome   Outcome     `json:"outcome"`
	// Explanation is the user-safe text from the invariant.
	Explanation string `json:"explanation"`
	// Detail is operator-facing context about why the invariant fired. It is
	// never shown to the user unredacted and never carries model output.
	Detail string `json:"detail"`
}

// Verdict is the complete, auditable result of a constitutional evaluation.
type Verdict struct {
	DecisionID string  `json:"decision_id"`
	Outcome    Outcome `json:"outcome"`
	// Reason is the code for the governing finding, or ReasonAllowed.
	Reason ReasonCode `json:"reason"`
	// Findings lists every invariant that fired, most restrictive first.
	Findings []Finding `json:"findings,omitempty"`
	// AdvisoryStatus records how model input was treated.
	AdvisoryStatus AdvisoryStatus `json:"advisory_status"`
	// AdvisoryReason explains a discarded advisory.
	AdvisoryReason ReasonCode `json:"advisory_reason,omitempty"`
	// BindingDigest is the identity any approval for this decision must match.
	BindingDigest string `json:"binding_digest"`
	// ConstitutionVersion is the version the verdict was reached under.
	ConstitutionVersion Version   `json:"constitution_version"`
	EvaluatedAt         time.Time `json:"evaluated_at"`
}

// Blocked reports whether the decision was refused outright.
func (v Verdict) Blocked() bool { return v.Outcome == OutcomeBlock }

// HardViolations returns the findings that can never be approved away.
func (v Verdict) HardViolations() []Finding {
	var hard []Finding
	for _, finding := range v.Findings {
		if finding.Severity == SeverityHard {
			hard = append(hard, finding)
		}
	}
	return hard
}

// RuntimeState is the deterministic, MARSHAL-owned view of the world at
// evaluation time. Every field is something MARSHAL observed or recorded
// itself. Nothing here is supplied by a model, which is what allows the gate
// to reach a trustworthy verdict even when the advisory input is hostile.
type RuntimeState struct {
	// RuntimeConstitution is the version this build implements.
	RuntimeConstitution Version

	// SandboxAvailable reports whether required isolation is actually present.
	SandboxAvailable bool
	// NetworkEnforced reports whether egress crosses an endpoint-enforcing
	// backend. A permissive network with no enforcement is not "enforced".
	NetworkEnforced bool

	// ApprovalPresent, ApprovalDigest, ApprovalActor and ApprovalExpiry
	// describe an approval held for this decision, as recorded by MARSHAL.
	ApprovalPresent bool
	ApprovalDigest  string
	ApprovalActor   string
	ApprovalExpiry  time.Time

	// AuthorizedActor reports whether authz cleared this actor for this action.
	AuthorizedActor bool

	// CapabilitiesGranted lists capability grants MARSHAL actually holds.
	CapabilitiesGranted []string

	// EvidenceFresh reports whether the supporting evidence still describes
	// the current state. EvidencePresent reports whether there is any.
	EvidencePresent bool
	EvidenceFresh   bool

	// ScopeRoots are the canonical roots the session may touch.
	ScopeRoots []string
	// ScopeViolations are scope entries MARSHAL determined lie outside the
	// roots. They are computed deterministically by the caller's path
	// resolution, not inferred from the request text.
	ScopeViolations []string

	// ForeignProjectRefs are references to state owned by another project.
	ForeignProjectRefs []string

	// AcceptanceCriteriaTotal and AcceptanceCriteriaMet drive completion.
	AcceptanceCriteriaTotal int
	AcceptanceCriteriaMet   int
	// IndependentReviewRequired and IndependentReviewDone drive separation of
	// duties on high-risk completion.
	IndependentReviewRequired bool
	IndependentReviewDone     bool

	// RollbackVerified reports that a restored state was actually checked.
	RollbackVerified bool

	// MemoryCandidateValidated reports that a memory candidate passed
	// deterministic prevalidation and carries admissible evidence.
	MemoryCandidateValidated bool

	// SecretsDetected lists credential material found in the request payload.
	SecretsDetected []string

	// HarnessGovernance is the evidence-derived governance state.
	HarnessGovernance GovernanceState

	// NativeInstructionOverrides names harness- or project-native
	// instructions that attempted to override a MARSHAL rule.
	NativeInstructionOverrides []string

	// EntitlementValid reports whether an ULTRA grant is present and unexpired.
	EntitlementValid bool

	// Now is the evaluation clock, injected for determinism in tests.
	Now time.Time
}

// Evaluate applies the constitution to one decision and returns the verdict.
//
// The order of operations is the substance of Process 00. Deterministic checks
// run first and decide the outcome (Article V). The advisory is consulted only
// afterwards, and only in the direction of caution: it can raise an outcome to
// something more restrictive, never lower one. That single asymmetry is what
// makes "any qualified AI may think for MARSHAL, no AI may redefine MARSHAL"
// hold mechanically rather than by convention.
func Evaluate(registry *Registry, envelope Envelope, state RuntimeState, advisory *Advisory) Verdict {
	now := state.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	verdict := Verdict{
		DecisionID:          envelope.DecisionID,
		ConstitutionVersion: envelope.ConstitutionVersion,
		EvaluatedAt:         now,
		Outcome:             OutcomeBlock,
		Reason:              ReasonEnvelopeInvalid,
		AdvisoryStatus:      AdvisoryAbsent,
	}
	if registry == nil {
		verdict.Reason = ReasonMissingCriticalData
		return verdict
	}
	// An envelope MARSHAL cannot fully understand is refused rather than
	// evaluated under guessed defaults.
	if err := envelope.Validate(); err != nil {
		verdict.Findings = []Finding{{
			Invariant: InvTruthfulControlSurface, Severity: SeverityHard,
			Reason: ReasonEnvelopeInvalid, Outcome: OutcomeBlock,
			Explanation: "The request is incomplete and cannot be evaluated.",
			Detail:      err.Error(),
		}}
		return verdict
	}
	verdict.BindingDigest = envelope.BindingDigest()

	findings := deterministicFindings(registry, envelope, state, now)

	// The advisory is read after the deterministic verdict is already formed.
	status, advisoryReason := ValidateAdvisory(Brief{
		Envelope:          envelope,
		OriginalRequest:   "-",
		HarnessGovernance: state.HarnessGovernance,
	}, advisory)
	verdict.AdvisoryStatus = status
	if status == AdvisoryDiscarded {
		verdict.AdvisoryReason = advisoryReason
	}
	if status == AdvisoryAccepted {
		findings = append(findings, advisoryEscalations(registry, envelope, advisory)...)
	}

	verdict.Findings = rankFindings(findings)
	verdict.Outcome, verdict.Reason = resolveOutcome(verdict.Findings)
	return verdict
}

// deterministicFindings runs every invariant that MARSHAL can decide from its
// own recorded state, with no model input involved.
func deterministicFindings(registry *Registry, envelope Envelope, state RuntimeState, now time.Time) []Finding {
	var findings []Finding
	add := func(id InvariantID, detail string) {
		inv, ok := registry.Lookup(id)
		if !ok || !inv.AppliesTo(envelope.Domain) {
			return
		}
		findings = append(findings, Finding{
			Invariant: inv.ID, Severity: inv.Severity, Reason: inv.Reason,
			Outcome: outcomeForSeverity(inv.Severity), Explanation: inv.Explanation, Detail: detail,
		})
	}

	// CI-014: the session's constitution must be one this runtime implements.
	if !state.RuntimeConstitution.IsZero() && !envelope.ConstitutionVersion.CompatibleWith(state.RuntimeConstitution) {
		add(InvConstitutionVersionBinding, "session is bound to "+envelope.ConstitutionVersion.String()+
			", runtime implements "+state.RuntimeConstitution.String())
	}

	// CI-011: no reference may reach into another project.
	if len(state.ForeignProjectRefs) > 0 {
		add(InvProjectIsolation, "references outside project "+envelope.ProjectID+": "+
			strings.Join(state.ForeignProjectRefs, ", "))
	}

	// CI-020: credential material never travels in a governed request.
	if len(state.SecretsDetected) > 0 {
		add(InvSecretsNotCanonical, "credential material detected in the request")
	}

	// CI-015: native configuration cannot outrank MARSHAL.
	if len(state.NativeInstructionOverrides) > 0 {
		add(InvNativeConfigSubordinate, "native instructions attempted an override: "+
			strings.Join(state.NativeInstructionOverrides, ", "))
	}

	// CI-005 / CI-006: isolation is fail-closed for anything that executes or
	// reaches the network. Absence of enforcement blocks; it never degrades to
	// running unprotected.
	if !state.SandboxAvailable && requiresSandbox(envelope.Domain) {
		add(InvSandboxFailClosed, "sandbox isolation is unavailable")
	}
	if !state.NetworkEnforced && requiresNetworkEnforcement(envelope.Domain) {
		add(InvNetworkFailClosed, "endpoint-enforcing egress is unavailable")
	}

	// Authorization precedes execution and is never minted by a model.
	if !state.AuthorizedActor && envelope.Domain.Mutating() {
		findings = append(findings, Finding{
			Invariant: InvAuthorityPrecedence, Severity: SeverityHard,
			Reason: ReasonAuthorizationDenied, Outcome: OutcomeBlock,
			Explanation: "The requester does not hold the authority for this action.",
			Detail:      "actor " + envelope.Actor + " is not authorized for " + string(envelope.Domain),
		})
	}

	// CI-007: scope violations computed by path resolution, not inferred.
	if len(state.ScopeViolations) > 0 {
		add(InvNoSilentScopeExpansion, "outside the agreed scope: "+strings.Join(state.ScopeViolations, ", "))
	}

	// CI-002 / CI-003: approval integrity. An approval binds to the exact
	// action; a changed action, an expired approval, or an approval granted by
	// the requester themselves is no approval at all.
	if state.ApprovalPresent {
		if state.ApprovalDigest != envelope.BindingDigest() {
			add(InvApprovalIntegrity, "the approval on file was granted for a different action")
		}
		if !state.ApprovalExpiry.IsZero() && !now.Before(state.ApprovalExpiry) {
			add(InvApprovalIntegrity, "the approval on file has expired")
		}
		if state.ApprovalActor != "" && state.ApprovalActor == envelope.Actor {
			add(InvNoSelfApproval, "the approval was granted by the requester")
		}
	}

	// CI-008 / CI-009: evidence must exist and still describe current state.
	if requiresEvidence(envelope.Domain) {
		if !state.EvidencePresent {
			add(InvEvidenceBackedClaims, "no evidence supports this decision")
		} else if !state.EvidenceFresh {
			add(InvEvidenceFreshness, "the supporting evidence predates the current state")
		}
	}

	// CI-010: a memory candidate reaches canonical memory only after
	// deterministic validation.
	if envelope.Domain == DomainMemoryPromotion && !state.MemoryCandidateValidated {
		add(InvMemoryPromotionGoverned, "the memory candidate has not passed deterministic validation")
	}

	// CI-012: completion is policy. Every acceptance criterion must be met and
	// any required independent review must have happened.
	if envelope.Domain == DomainCompletion {
		if state.AcceptanceCriteriaTotal == 0 {
			add(InvCompletionIsPolicy, "no acceptance criteria are defined to verify against")
		} else if state.AcceptanceCriteriaMet < state.AcceptanceCriteriaTotal {
			add(InvCompletionIsPolicy, "acceptance criteria are not fully met")
		}
		if state.IndependentReviewRequired && !state.IndependentReviewDone {
			add(InvCompletionIsPolicy, "the required independent review has not happened")
		}
	}

	// CI-017: rollback is reported only once the restored state was checked.
	if envelope.Domain == DomainRollback && !state.RollbackVerified {
		add(InvRollbackTruthful, "the restored state has not been verified")
	}

	// CI-018: ULTRA raises capability and never lowers a guarantee. An ULTRA
	// decision without a valid entitlement is refused outright, so a local
	// flag cannot confer ULTRA powers.
	if envelope.Mode == ModeUltra && !state.EntitlementValid {
		add(InvUltraNoWeakening, "ULTRA was requested without a valid entitlement")
	}

	// A mutating action must be recoverable or must disclose that it is not.
	if envelope.Domain.Mutating() && envelope.Reversibility.RequiresRestorePlan() &&
		envelope.CheckpointID == "" && len(envelope.ExternalEffects) == 0 {
		findings = append(findings, Finding{
			Invariant: InvRollbackTruthful, Severity: SeverityApproval,
			Reason: ReasonApprovalRequired, Outcome: OutcomeRequireApproval,
			Explanation: "This change cannot be undone automatically and needs confirmation.",
			Detail:      "reversibility " + string(envelope.Reversibility) + " with no checkpoint and no disclosed external effect",
		})
	}

	// Degraded harness governance is reported honestly rather than assumed
	// away. It reduces operation; it never blocks recovery surfaces.
	switch state.HarnessGovernance {
	case GovernanceDegraded, GovernanceUnverified:
		findings = append(findings, Finding{
			Invariant: InvNativeConfigSubordinate, Severity: SeverityDegrade,
			Reason: ReasonAllowed, Outcome: OutcomeDegrade,
			Explanation: "MARSHAL cannot fully confirm control of the tool in use, so it is operating in a reduced mode.",
			Detail:      "harness governance is " + string(state.HarnessGovernance),
		})
	case GovernanceUnavailable:
		if envelope.Domain.Mutating() {
			findings = append(findings, Finding{
				Invariant: InvNativeConfigSubordinate, Severity: SeverityHard,
				Reason: ReasonNativeInstructionRefused, Outcome: OutcomeBlock,
				Explanation: "The tool required for this action cannot be governed, so the action is blocked.",
				Detail:      "harness governance is unavailable",
			})
		}
	}

	return findings
}

// advisoryEscalations converts a model's own admissions into escalations.
//
// Only admissions are read. A self-check that claims everything is fine
// produces nothing here, so a model gains no ground by asserting its own
// compliance; a model that honestly flags a problem causes MARSHAL to be more
// careful, which is the behaviour worth encouraging.
func advisoryEscalations(registry *Registry, envelope Envelope, advisory *Advisory) []Finding {
	var findings []Finding
	appendFinding := func(id InvariantID, outcome Outcome, reason ReasonCode, detail string) {
		inv, ok := registry.Lookup(id)
		if !ok {
			return
		}
		findings = append(findings, Finding{
			Invariant: inv.ID, Severity: inv.Severity, Reason: reason,
			Outcome: outcome, Explanation: inv.Explanation, Detail: detail,
		})
	}

	check := advisory.SelfCheck
	if check.ExpandsScope {
		appendFinding(InvNoSilentScopeExpansion, OutcomeRequireApproval, ReasonScopeEscape,
			"the recommendation states that it expands scope")
	}
	if !check.AlignedWithGoal {
		appendFinding(InvGoalFidelity, OutcomeReplan, ReasonGoalDrift,
			"the recommendation states that it is not aligned with the goal")
	}
	if !check.RespectsConstraints {
		appendFinding(InvAuthorityPrecedence, OutcomeBlock, ReasonAuthorityPrecedenceViolated,
			"the recommendation states that it does not respect hard constraints")
	}
	if !check.EvidenceSufficient || check.DependsOnUnknowns || check.DependsOnStaleContext {
		appendFinding(InvEvidenceBackedClaims, OutcomeRequireVerification, ReasonEvidenceInsufficient,
			"the recommendation depends on unverified, unknown or stale input")
	}
	if check.IncreasesPrivilege {
		appendFinding(InvAuthorityPrecedence, OutcomeRequireApproval, ReasonApprovalRequired,
			"the recommendation states that it increases privilege")
	}
	if check.Destructive && !check.RecoveryAvailable {
		appendFinding(InvRollbackTruthful, OutcomeRequireApproval, ReasonApprovalRequired,
			"the recommendation states that it is destructive with no recovery")
	}
	// A model may always ask for more scrutiny.
	if advisory.RecommendsApproval {
		appendFinding(InvNoSilentScopeExpansion, OutcomeRequireApproval, ReasonApprovalRequired,
			"the recommendation asks for human approval")
	}
	if advisory.RecommendsReviewer {
		appendFinding(InvEvidenceBackedClaims, OutcomeRequireVerification, ReasonEvidenceInsufficient,
			"the recommendation asks for independent review")
	}
	// A proposal outside the envelope's domain is out of contract.
	if advisory.ProposedAction != "" && envelope.Domain == DomainRead {
		appendFinding(InvAIMayNotAuthorize, OutcomeBlock, ReasonAIAuthorityRefused,
			"a mutation was proposed for a read-only decision")
	}
	return findings
}

func outcomeForSeverity(severity Severity) Outcome {
	switch severity {
	case SeverityHard:
		return OutcomeBlock
	case SeverityApproval:
		return OutcomeRequireApproval
	case SeverityDegrade:
		return OutcomeDegrade
	default:
		return OutcomeBlock
	}
}

// requiresSandbox reports the domains that must not run without isolation.
func requiresSandbox(domain Domain) bool {
	switch domain {
	case DomainShell, DomainFileMutation, DomainExternalEffect:
		return true
	default:
		return false
	}
}

// requiresNetworkEnforcement reports the domains that must not reach the
// network without an endpoint-enforcing backend.
func requiresNetworkEnforcement(domain Domain) bool {
	switch domain {
	case DomainNetwork, DomainExternalEffect:
		return true
	default:
		return false
	}
}

// requiresEvidence reports the domains where an assertion is not proof.
func requiresEvidence(domain Domain) bool {
	switch domain {
	case DomainCompletion, DomainVerification, DomainMemoryPromotion, DomainRollback:
		return true
	default:
		return false
	}
}

// rankFindings orders findings most restrictive first so that the governing
// finding is always the first element.
func rankFindings(findings []Finding) []Finding {
	if len(findings) == 0 {
		return nil
	}
	sort.SliceStable(findings, func(a, b int) bool {
		if findings[a].Outcome.rank() != findings[b].Outcome.rank() {
			return findings[a].Outcome.rank() > findings[b].Outcome.rank()
		}
		return findings[a].Invariant < findings[b].Invariant
	})
	return findings
}

// resolveOutcome picks the most restrictive outcome among the findings. With
// no findings the decision is allowed, which is safe because reaching this
// point means every applicable invariant was checked and none fired.
func resolveOutcome(findings []Finding) (Outcome, ReasonCode) {
	if len(findings) == 0 {
		return OutcomeAllow, ReasonAllowed
	}
	governing := findings[0]
	return governing.Outcome, governing.Reason
}
