//go:build governed

package startup

const networkReadySummary = "Outbound access is limited to approved destinations."

// blockedHelpBody is the "Why work can be blocked" help text.
const blockedHelpBody = "Work is blocked when running it would not be safe or would not be reproducible: " +
	"no project, no Git, no sandbox to isolate changes, or no policy saying what is permitted. " +
	"Blocking work does not block MARSHAL. You keep Setup, Doctor, Help, your project history and recovery."

// probeIsolation reports sandbox and network enforcement.
//
// Both are reported from what the runtime actually determined, never inferred
// from the presence of a binary. Neither is ever downgraded to "good enough":
// when isolation is unavailable, execution is blocked rather than allowed to
// proceed unprotected, which is the behaviour Process 00 requires.
func probeIsolation(env Environment) []Check {
	var checks []Check

	if env.SandboxEnforced {
		checks = append(checks, Check{
			ID: "env.sandbox", Dimension: DimensionEnvironment, Status: StatusReady,
			Required: true, Reason: ReasonOK,
			Summary: "Work runs in an isolated sandbox.",
		})
	} else {
		checks = append(checks, Check{
			ID: "env.sandbox", Dimension: DimensionEnvironment, Status: StatusMissing,
			Required: true, Reason: ReasonSandboxUnavailable,
			Summary:      "Sandbox isolation is not available.",
			Impact:       "Work is blocked rather than run without isolation.",
			Remedy:       Describe(ReasonSandboxUnavailable).Remedy,
			Capabilities: []Capability{CapProjectExecution, CapProviderExecution},
		})
	}

	if env.NetworkEnforced {
		checks = append(checks, Check{
			ID: "env.network", Dimension: DimensionEnvironment, Status: StatusReady,
			Required: false, Reason: ReasonOK,
			Summary: networkReadySummary,
		})
	} else {
		// Unenforced egress removes network and provider capability but does
		// not block local work, so it is not marked Required. Reporting it as
		// LIMITED would overstate it: enforcement is absent, not partial.
		checks = append(checks, Check{
			ID: "env.network", Dimension: DimensionEnvironment, Status: StatusMissing,
			Required: false, Reason: ReasonNetworkUnenforced,
			Summary:      "Outbound access cannot be restricted to approved destinations.",
			Impact:       "Features that reach the network are unavailable. Local work continues.",
			Remedy:       Describe(ReasonNetworkUnenforced).Remedy,
			Capabilities: []Capability{CapNetworkEgress, CapProviderExecution},
		})
	}
	return checks
}
