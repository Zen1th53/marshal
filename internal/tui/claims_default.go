//go:build governed

package tui

// Text that says what the governed build does. claims_extreme.go holds
// the Marshal edition's wording.
const (
	securityKeyWhat    = "security: egress, leases and audit"
	egressStatusHeader = "Governed workers: isolated network namespace; per-run Unix proxy; exact endpoints; operator grants only.\nNative sessions opened directly by the operator: out of scope.\n"
	sandboxStatusText  = "Sandbox status: NOT VERIFIED. Isolation is established and reported per runtime execution, not by the TUI."
	providerEgressLine = "    Egress:  governed cells use --unshare-net and a per-run Unix proxy (requires working bubblewrap and trusted socat); unapproved endpoints BLOCKED_BY_POLICY (/egress); native sessions opened directly by the operator OUT_OF_SCOPE\n"

	egressReportsEnforcement = true
	egressNoRunsText         = "No active governed egress runs.\n"
	// showQualificationLabel: a provider command MARSHAL has not qualified
	// says so on its answer.
	showQualificationLabel = true
	providerStatusHeading  = "PROVIDER / HARNESS STATUS:"
)

// credentialRequestQueuedText answers /permission credential request.
func credentialRequestQueuedText(string) string {
	return "Credential broker permission queued. A allows; every other key denies. Retry the governed task after allowing."
}

// credentialRevocationMessage answers /permission credential revoke and
// status from the revocation's state.
func credentialRevocationMessage(provider, state string) string {
	switch state {
	case "pending":
		return "Credential broker revoked for " + provider + "; active broker connections pending owner acknowledgement. Check /permission credential status " + provider + "."
	case "closed":
		return "Credential broker revoked for " + provider + "; active broker connections closed (owners acknowledged)."
	default:
		return "No credential revocation recorded for " + provider + "."
	}
}
