package constitution

import (
	"sort"
	"time"
)

// ViolationClass names a constitutional violation. These are first-class
// security events (Article XXV), not warnings: each one means a control that
// should have held did not, and each carries a mandated response.
type ViolationClass string

const (
	ViolationGoalDrift             ViolationClass = "GOAL_DRIFT"
	ViolationScopeEscape           ViolationClass = "SCOPE_ESCAPE"
	ViolationAuthzBypass           ViolationClass = "AUTHZ_BYPASS"
	ViolationApprovalBypass        ViolationClass = "APPROVAL_BYPASS"
	ViolationSandboxBypass         ViolationClass = "SANDBOX_BYPASS"
	ViolationNetworkBypass         ViolationClass = "NETWORK_BYPASS"
	ViolationEvidenceFabrication   ViolationClass = "EVIDENCE_FABRICATION"
	ViolationStaleEvidenceUse      ViolationClass = "STALE_EVIDENCE_USE"
	ViolationMemoryPoisoning       ViolationClass = "MEMORY_POISONING"
	ViolationCrossProjectLeak      ViolationClass = "CROSS_PROJECT_LEAK"
	ViolationFalseSuccess          ViolationClass = "FALSE_SUCCESS"
	ViolationProviderGovernanceLos ViolationClass = "PROVIDER_GOVERNANCE_LOSS"
	ViolationVersionMismatch       ViolationClass = "VERSION_MISMATCH"
)

// Response is the mandated reaction to a violation. It is derived from the
// class, never chosen per incident, so the same violation always draws the
// same response regardless of who reports it or how convenient it is.
type Response string

const (
	// ResponseBlock refuses the action and lets the session continue.
	ResponseBlock Response = "BLOCK"
	// ResponseRevoke additionally withdraws the capability or approval that
	// was misused.
	ResponseRevoke Response = "REVOKE"
	// ResponseSuspend halts the session pending a human decision. It is
	// reserved for violations that call the session's integrity into question.
	ResponseSuspend Response = "SUSPEND"
	// ResponseInvalidate marks derived state untrustworthy so it cannot be
	// relied on downstream.
	ResponseInvalidate Response = "INVALIDATE"
	// ResponseReview requires independent human review before continuing.
	ResponseReview Response = "REVIEW"
)

// Violation is a recorded constitutional breach.
type Violation struct {
	Class     ViolationClass `json:"class"`
	Invariant InvariantID    `json:"invariant"`
	Response  Response       `json:"response"`

	ProjectID  string `json:"project_id"`
	SessionID  string `json:"session_id"`
	DecisionID string `json:"decision_id,omitempty"`
	Actor      string `json:"actor,omitempty"`

	// Detail is operator-facing. It never carries secrets, raw provider output
	// or hidden reasoning.
	Detail     string    `json:"detail"`
	DetectedAt time.Time `json:"detected_at"`
	Surface    Surface   `json:"surface,omitempty"`
	Version    Version   `json:"constitution_version"`
	// InvalidatesEvidence lists evidence that must be treated as untrustworthy
	// as a result of this violation.
	InvalidatesEvidence []string `json:"invalidates_evidence,omitempty"`
}

// responseFor maps a violation class onto its mandated response.
//
// The mapping is total and fixed. Violations that indicate the session itself
// may be compromised suspend it; violations that indicate a misused grant
// revoke it; violations that poison derived state invalidate it.
func responseFor(class ViolationClass) Response {
	switch class {
	case ViolationAuthzBypass, ViolationApprovalBypass, ViolationSandboxBypass, ViolationNetworkBypass:
		// A bypass of a hard control means the control was circumvented, not
		// merely refused. The grant that permitted the attempt is withdrawn.
		return ResponseRevoke
	case ViolationEvidenceFabrication, ViolationMemoryPoisoning, ViolationCrossProjectLeak:
		// These call the session's integrity into question: work already done
		// under it cannot be trusted without review.
		return ResponseSuspend
	case ViolationStaleEvidenceUse, ViolationFalseSuccess:
		return ResponseInvalidate
	case ViolationGoalDrift, ViolationScopeEscape:
		return ResponseReview
	case ViolationProviderGovernanceLos, ViolationVersionMismatch:
		return ResponseBlock
	default:
		// An unrecognised class is treated as the most serious case rather
		// than the least.
		return ResponseSuspend
	}
}

// ResponseFor exposes the mandated response for a violation class.
func ResponseFor(class ViolationClass) Response { return responseFor(class) }

// Halting reports whether the response stops the session rather than just the
// action.
func (r Response) Halting() bool { return r == ResponseSuspend }

// classForReason maps a gate reason code onto the violation class it evidences.
// Reasons that represent a legitimate refusal rather than a breach map to no
// class: being correctly told "you need approval" is the system working, and
// recording it as a security violation would drown the real ones.
var classForReason = map[ReasonCode]ViolationClass{
	ReasonAuthorizationDenied:         ViolationAuthzBypass,
	ReasonSelfApproval:                ViolationApprovalBypass,
	ReasonApprovalStale:               ViolationApprovalBypass,
	ReasonSandboxUnavailable:          ViolationSandboxBypass,
	ReasonNetworkNotEnforced:          ViolationNetworkBypass,
	ReasonScopeEscape:                 ViolationScopeEscape,
	ReasonGoalDrift:                   ViolationGoalDrift,
	ReasonEvidenceStale:               ViolationStaleEvidenceUse,
	ReasonMemoryPromotionRefused:      ViolationMemoryPoisoning,
	ReasonCrossProjectLeak:            ViolationCrossProjectLeak,
	ReasonFalseSuccess:                ViolationFalseSuccess,
	ReasonConstitutionVersionMismatch: ViolationVersionMismatch,
	ReasonNativeInstructionRefused:    ViolationProviderGovernanceLos,
	ReasonAIAuthorityRefused:          ViolationAuthzBypass,
	ReasonUltraCannotWeaken:           ViolationAuthzBypass,
	ReasonSecretExposure:              ViolationEvidenceFabrication,
}

// ViolationsFrom derives the violations evidenced by a verdict.
//
// Only findings that indicate a control was circumvented become violations. A
// verdict that merely requires an approval, or that degrades honestly, is the
// constitution working as intended and produces nothing here.
func ViolationsFrom(verdict Verdict, envelope Envelope, now time.Time) []Violation {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	seen := make(map[ViolationClass]bool)
	var violations []Violation
	for _, finding := range verdict.Findings {
		class, ok := classForReason[finding.Reason]
		if !ok || seen[class] {
			continue
		}
		// A degrade is an honest reduction in capability, not a breach.
		if finding.Outcome == OutcomeDegrade {
			continue
		}
		seen[class] = true
		violations = append(violations, Violation{
			Class:      class,
			Invariant:  finding.Invariant,
			Response:   responseFor(class),
			ProjectID:  envelope.ProjectID,
			SessionID:  envelope.SessionID,
			DecisionID: envelope.DecisionID,
			Actor:      envelope.Actor,
			Detail:     finding.Detail,
			DetectedAt: now,
			Surface:    envelope.Surface,
			Version:    envelope.ConstitutionVersion,
		})
	}
	sort.SliceStable(violations, func(a, b int) bool { return violations[a].Class < violations[b].Class })
	return violations
}

// MostSevereResponse returns the strongest response required by a set of
// violations, so a session is never allowed to continue on the strength of the
// mildest breach it committed.
func MostSevereResponse(violations []Violation) (Response, bool) {
	if len(violations) == 0 {
		return "", false
	}
	rank := map[Response]int{
		ResponseBlock: 1, ResponseReview: 2, ResponseInvalidate: 3,
		ResponseRevoke: 4, ResponseSuspend: 5,
	}
	strongest := violations[0].Response
	for _, violation := range violations[1:] {
		if rank[violation.Response] > rank[strongest] {
			strongest = violation.Response
		}
	}
	return strongest, true
}
