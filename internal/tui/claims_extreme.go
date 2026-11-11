//go:build !governed

package tui

// Text that says what this build does. The Marshal build runs workers on
// the host with no sandbox and no network or credential broker, and the
// Marshal approves plans and delivery itself, so nothing here claims
// otherwise. claims_default.go holds the governed edition's wording.
const (
	bpD1 = "the Marshal approves the plan (or asks you: y)"
	bpD3 = "decision due? → Marshal decides or asks you"
	bpU5 = "the Marshal delivers the verified result"

	securityKeyWhat    = "security: network calls and audit"
	egressStatusHeader = "Workers run on this computer and use your network connection directly. Marshal lists the network calls it records; it does not block them.\n"
	sandboxStatusText  = "No sandbox: workers run on this computer as you, each task in its own Git worktree."
	providerEgressLine = "    Network: the worker uses your normal network connection\n"

	// egressReportsEnforcement: /egress probes and reports bubblewrap
	// network isolation only where the build uses it.
	egressReportsEnforcement = false
)

// archComponents is what each part does in this build.
var archComponents = [][2]string{
	{"Workspace / TUI", "Board, chat dock, inbox; owner input only"}, {"Marshal", "plans, reviews, never edits task files"},
	{"Runtime", "starts a worker per task, then review and checks"}, {"Worktrees", "one per task; only verified work merges"},
	{"Memory / MCP", "durable only after review; recalls audited"}, {"Audit", "append-only records"}, {"Host watch", "thermal, disk, memory policy every second"},
}
