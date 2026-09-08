package constitution

// ReasonCode is a stable machine code explaining a constitutional outcome.
// Codes are part of the cross-surface contract: TUI, CLI, Web, MCP and A2A all
// report the same code for the same decision, so a user never sees a softer
// explanation on one surface than another (Article XI).
type ReasonCode string

const (
	ReasonAllowed ReasonCode = "CONST_ALLOWED"

	ReasonAIAuthorityRefused          ReasonCode = "CONST_AI_AUTHORITY_REFUSED"
	ReasonApprovalRequired            ReasonCode = "CONST_APPROVAL_REQUIRED"
	ReasonApprovalStale               ReasonCode = "CONST_APPROVAL_STALE"
	ReasonSelfApproval                ReasonCode = "CONST_SELF_APPROVAL"
	ReasonAuthorityPrecedenceViolated ReasonCode = "CONST_AUTHORITY_PRECEDENCE_VIOLATED"
	ReasonAuthorizationDenied         ReasonCode = "CONST_AUTHORIZATION_DENIED"
	ReasonSandboxUnavailable          ReasonCode = "CONST_SANDBOX_UNAVAILABLE"
	ReasonNetworkNotEnforced          ReasonCode = "CONST_NETWORK_NOT_ENFORCED"
	ReasonScopeEscape                 ReasonCode = "CONST_SCOPE_ESCAPE"
	ReasonEvidenceInsufficient        ReasonCode = "CONST_EVIDENCE_INSUFFICIENT"
	ReasonEvidenceStale               ReasonCode = "CONST_EVIDENCE_STALE"
	ReasonMemoryPromotionRefused      ReasonCode = "CONST_MEMORY_PROMOTION_REFUSED"
	ReasonCrossProjectLeak            ReasonCode = "CONST_CROSS_PROJECT_LEAK"
	ReasonCompletionNotAuthorized     ReasonCode = "CONST_COMPLETION_NOT_AUTHORIZED"
	ReasonFalseSuccess                ReasonCode = "CONST_FALSE_SUCCESS"
	ReasonConstitutionVersionMismatch ReasonCode = "CONST_VERSION_MISMATCH"
	ReasonNativeInstructionRefused    ReasonCode = "CONST_NATIVE_INSTRUCTION_REFUSED"
	ReasonGoalDrift                   ReasonCode = "CONST_GOAL_DRIFT"
	ReasonRollbackUnverified          ReasonCode = "CONST_ROLLBACK_UNVERIFIED"
	ReasonUltraCannotWeaken           ReasonCode = "CONST_ULTRA_CANNOT_WEAKEN"
	ReasonInventedConfidence          ReasonCode = "CONST_INVENTED_CONFIDENCE"
	ReasonSecretExposure              ReasonCode = "CONST_SECRET_EXPOSURE"
	ReasonEnvelopeInvalid             ReasonCode = "CONST_ENVELOPE_INVALID"
	ReasonMissingCriticalData         ReasonCode = "CONST_MISSING_CRITICAL_DATA"
	ReasonAdvisoryMalformed           ReasonCode = "CONST_ADVISORY_MALFORMED"
)

// ReasonInfo carries the operator-facing and user-facing description of a code
// along with its retry semantics.
type ReasonInfo struct {
	Code ReasonCode `json:"code"`
	// Retryable reports whether repeating the identical request, with state,
	// approvals and evidence unchanged, could produce a different outcome. It
	// is false for every constitutional refusal: a refusal that flipped on a
	// bare retry would be a bypass, not a decision. Codes whose Recovery names
	// a concrete change are unblocked by making that change, not by retrying.
	Retryable bool `json:"retryable"`
	// Recovery names the action that would legitimately unblock the request.
	Recovery string `json:"recovery"`
}

var reasonCatalog = map[ReasonCode]ReasonInfo{
	ReasonAllowed:                     {Code: ReasonAllowed, Retryable: false, Recovery: "No action needed."},
	ReasonAIAuthorityRefused:          {Code: ReasonAIAuthorityRefused, Retryable: false, Recovery: "Route the action through the MARSHAL decision that owns it."},
	ReasonApprovalRequired:            {Code: ReasonApprovalRequired, Retryable: false, Recovery: "Obtain an approval bound to this exact action."},
	ReasonApprovalStale:               {Code: ReasonApprovalStale, Retryable: false, Recovery: "Request a fresh approval for the changed action."},
	ReasonSelfApproval:                {Code: ReasonSelfApproval, Retryable: false, Recovery: "Have a different principal approve or review the action."},
	ReasonAuthorityPrecedenceViolated: {Code: ReasonAuthorityPrecedenceViolated, Retryable: false, Recovery: "Change the higher-authority rule through its own governed path, or drop the conflicting request."},
	ReasonAuthorizationDenied:         {Code: ReasonAuthorizationDenied, Retryable: false, Recovery: "Use a principal holding the required authority."},
	ReasonSandboxUnavailable:          {Code: ReasonSandboxUnavailable, Retryable: false, Recovery: "Restore sandbox isolation. Do not disable isolation to proceed."},
	ReasonNetworkNotEnforced:          {Code: ReasonNetworkNotEnforced, Retryable: false, Recovery: "Restore endpoint-enforcing egress. Do not open unrestricted network access to proceed."},
	ReasonScopeEscape:                 {Code: ReasonScopeEscape, Retryable: false, Recovery: "Narrow the action to the agreed scope, or get the scope extended explicitly."},
	ReasonEvidenceInsufficient:        {Code: ReasonEvidenceInsufficient, Retryable: false, Recovery: "Produce verifiable evidence for the claim."},
	ReasonEvidenceStale:               {Code: ReasonEvidenceStale, Retryable: false, Recovery: "Re-run the verification against the current state."},
	ReasonMemoryPromotionRefused:      {Code: ReasonMemoryPromotionRefused, Retryable: false, Recovery: "Submit the fact as a memory candidate with supporting evidence."},
	ReasonCrossProjectLeak:            {Code: ReasonCrossProjectLeak, Retryable: false, Recovery: "Use state belonging to the current project."},
	ReasonCompletionNotAuthorized:     {Code: ReasonCompletionNotAuthorized, Retryable: false, Recovery: "Satisfy the outstanding acceptance criteria and reviews."},
	ReasonFalseSuccess:                {Code: ReasonFalseSuccess, Retryable: false, Recovery: "Report the state that the backend actually reached."},
	ReasonConstitutionVersionMismatch: {Code: ReasonConstitutionVersionMismatch, Retryable: false, Recovery: "Run a runtime implementing the session's constitution version, or migrate the session deliberately."},
	ReasonNativeInstructionRefused:    {Code: ReasonNativeInstructionRefused, Retryable: false, Recovery: "Express the requirement as MARSHAL policy instead of tool-native configuration."},
	ReasonGoalDrift:                   {Code: ReasonGoalDrift, Retryable: false, Recovery: "Revise the goal explicitly, or bring the step back in line with it."},
	ReasonRollbackUnverified:          {Code: ReasonRollbackUnverified, Retryable: false, Recovery: "Verify the restored state before reporting rollback."},
	ReasonUltraCannotWeaken:           {Code: ReasonUltraCannotWeaken, Retryable: false, Recovery: "Satisfy the requirement; ULTRA does not remove it."},
	ReasonInventedConfidence:          {Code: ReasonInventedConfidence, Retryable: false, Recovery: "Derive the figure from explicit acceptance criteria, or report UNKNOWN."},
	ReasonSecretExposure:              {Code: ReasonSecretExposure, Retryable: false, Recovery: "Remove the credential material from the request."},
	ReasonEnvelopeInvalid:             {Code: ReasonEnvelopeInvalid, Retryable: false, Recovery: "Supply a complete decision envelope."},
	ReasonMissingCriticalData:         {Code: ReasonMissingCriticalData, Retryable: false, Recovery: "Supply the missing canonical context."},
	ReasonAdvisoryMalformed:           {Code: ReasonAdvisoryMalformed, Retryable: true, Recovery: "Re-request a well-formed recommendation; the decision proceeds without it."},
}

// Describe returns the catalog entry for a reason code. Unknown codes are
// reported as non-retryable so that an unrecognised outcome fails closed.
func Describe(code ReasonCode) ReasonInfo {
	if info, ok := reasonCatalog[code]; ok {
		return info
	}
	return ReasonInfo{Code: code, Retryable: false, Recovery: "Unrecognised constitutional outcome; treat as blocked."}
}

// KnownReasonCodes returns every code in the catalog. Tests use it to assert
// that each invariant maps onto a described code.
func KnownReasonCodes() []ReasonCode {
	out := make([]ReasonCode, 0, len(reasonCatalog))
	for code := range reasonCatalog {
		out = append(out, code)
	}
	return out
}
