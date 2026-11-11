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
)
