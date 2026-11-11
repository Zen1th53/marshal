//go:build !governed

package tui

// Text that says what this build does. The Marshal build runs workers on
// the host with no sandbox and no network or credential broker, and the
// Marshal approves plans and delivery itself, so nothing here claims
// otherwise. claims_default.go holds the governed edition's wording.
const (
	securityKeyWhat    = "security: network calls and audit"
	egressStatusHeader = "Workers run on this computer and use your network connection directly. Marshal lists the network calls it records; it does not block them.\n"
	sandboxStatusText  = "No sandbox: workers run on this computer as you, each task in its own Git worktree."
	providerEgressLine = "    Network: the worker uses your normal network connection\n"

	// egressReportsEnforcement: /egress probes and reports bubblewrap
	// network isolation only where the build uses it.
	egressReportsEnforcement = false
)
