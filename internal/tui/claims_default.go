//go:build governed

package tui

// Text that says what the governed build does. claims_extreme.go holds
// the Marshal edition's wording.
const (
	bpD1 = "plan approved? (/marshal approve)"
	bpD3 = "interim approval due? → owner reviews"
	bpU5 = "owner accepts the result"

	securityKeyWhat    = "security: egress, leases and audit"
	egressStatusHeader = "Governed workers: isolated network namespace; per-run Unix proxy; exact endpoints; operator grants only.\nNative sessions opened directly by the operator: out of scope.\n"
	sandboxStatusText  = "Sandbox status: NOT VERIFIED. Isolation is established and reported per runtime execution, not by the TUI."
	providerEgressLine = "    Egress:  governed cells use --unshare-net and a per-run Unix proxy (requires working bubblewrap and trusted socat); unapproved endpoints BLOCKED_BY_POLICY (/egress); native sessions opened directly by the operator OUT_OF_SCOPE\n"

	egressReportsEnforcement = true
)

// archComponents is what each part may do.
var archComponents = [][2]string{
	{"Workspace / TUI", "Board, chat dock, inbox; owner input only"}, {"Marshal", "plans, reviews, never edits task files"},
	{"Runtime", "Process 05 governed lanes, approvals R2/R3"}, {"Worktrees", "one per lane; merge to integration on accept"},
	{"Egress broker", "manifest hosts only; others go to inbox"}, {"Credential broker", "short per-task leases; no auth files in lanes"},
	{"Memory / MCP", "durable only after review; recalls audited"}, {"Audit", "append-only records"}, {"Host watch", "thermal, disk, memory policy every second"},
}
