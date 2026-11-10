//go:build !governed

package startup

// The Marshal build runs workers on the host: there is no sandbox and no
// network broker to find, so both checks only say how work runs. They are
// never Required and never remove a capability. Startup must not refuse
// to open over a tool this build does not use.
const (
	hostSandboxSummary = "Workers run on this computer without a sandbox, with your account's file and network access."
	hostNetworkSummary = "Workers use your normal network connection. Marshal does not limit where they connect."
)

// blockedHelpBody is the "Why work can be blocked" help text.
const blockedHelpBody = "Work is blocked when it has nothing safe to run against: " +
	"no project, no Git, no first commit, or no policy saying what is permitted. " +
	"Blocking work does not block MARSHAL. You keep Setup, Doctor, Help, your project history and recovery."

// probeIsolation reports how work runs in this build. The environment's
// isolation flags are not consulted: nothing here depends on bubblewrap.
func probeIsolation(Environment) []Check {
	return []Check{
		{ID: "env.sandbox", Dimension: DimensionEnvironment, Status: StatusOptional, Required: false, Reason: ReasonOK, Summary: hostSandboxSummary},
		{ID: "env.network", Dimension: DimensionEnvironment, Status: StatusOptional, Required: false, Reason: ReasonOK, Summary: hostNetworkSummary},
	}
}
