package app

import (
	"github.com/Zen1th53/marshal/internal/adapter"
	"strings"
)

// infraRetryEscalation prefixes the escalation of a task whose attempts kept
// ending without a hand-in. It is never permanent: the operator may retry.
const infraRetryEscalation = "infrastructure retry limit reached"

// infraFailureKinds name, in lowercased worker or runtime output, a failure
// of the machinery around the work rather than of delivered work: the
// provider's sign-in, transport, harness or tool output, or the worker
// process itself. The first matching kind is reported.
var infraFailureKinds = []struct {
	kind    string
	markers []string
}{
	// Checked first: a platform that cannot host the worker is not a
	// passing network fault, and retrying or cooling providers cannot help.
	{platformUnsupported, []string{"on this platform", "requires linux", "requires private linux", "linux supervision"}},
	// A mutation lease that ran out is Marshal's own time bound on the
	// attempt, not the provider failing: checked before every provider kind.
	{workerLeaseExpired, []string{"execution lease expired"}},
	// The worker's stdio connection to Marshal's own MCP server broke. Like
	// a lease expiry it is not the provider's fault.
	{marshalToolsLost, []string{marshalToolsLostMarker}},
	// Marshal's own terminal (a tmux command that ran past its deadline on
	// a slow host) is not the provider crashing or its network failing.
	{terminalHostSlow, []string{"tmux ["}},
	{"provider quota or rate limit", []string{"usage limit", "rate limit", "rate-limit", "quota", "hit your limit", "5-hour limit", "too many requests", "resource_exhausted", "out of credits", "insufficient credits"}},
	{"provider cooldown or failover refusal", []string{"provider cooldown", "provider failover", "not limited by its provider", "provider execution failure"}},
	{"transport or network", []string{"connection refused", "connection reset", "broken pipe", "eof", "timeout", "timed out", "deadline exceeded", "tls", "dns", "network", "socket", "transport", "502", "503", "504", "overloaded", "unavailable"}},
	{"harness context or tool output", []string{"context budget", "context window", "context length", "skills context", "malformed", "invalid json", "unexpected end of json", "parse", "output invalid", "stream"}},
}

// classifyInfraFailure names the kind of failure that ended a worker attempt
// before it delivered a hand-in, with a bounded reason. Such an attempt is
// never a rejection of delivered work, so it is not rework; an unrecognised
// failure is reported as a worker process failure.
func classifyInfraFailure(text string) (kind, reason string) {
	reason = adapter.DiagnosticSummary(text, 1000)
	if isProviderAuthFailure(text) {
		return providerAuthFailure, reason
	}
	lower := strings.ToLower(text)
	for _, k := range infraFailureKinds {
		for _, marker := range k.markers {
			if strings.Contains(lower, marker) {
				return k.kind, reason
			}
		}
	}
	return "worker process failure", reason
}

// workerLeaseExpired is the failure kind of an attempt whose mutation lease
// ran out before it handed in.
const workerLeaseExpired = "task time ran out"

// marshalToolsLost is the failure kind of a worker whose connection to
// Marshal's MCP tools broke.
const marshalToolsLost = "connection to Marshal's tools lost"

// terminalHostSlow is the failure kind of an attempt cut short by Marshal's
// own terminal host (tmux) rather than by the provider.
const terminalHostSlow = "Marshal's terminal was slow"

// infraKindCoolsProvider reports whether a failure streak of this kind says
// something about the provider, so cooling it down for failover can help.
// A crash, an expired lease or a broken connection to Marshal's own tools
// never puts a provider into a cooldown: every provider would wait on a
// "quota" it still has.
func infraKindCoolsProvider(kind string) bool {
	switch kind {
	case workerLeaseExpired, marshalToolsLost, terminalHostSlow, "worker process failure", platformUnsupported, providerAuthFailure:
		return false
	}
	return true
}

// infraFailurePhrase is what happened to provider, as the Board says it.
func infraFailurePhrase(provider, kind string) string {
	switch kind {
	case workerLeaseExpired:
		return "the task's time ran out on " + provider
	case marshalToolsLost:
		return provider + " lost its connection to Marshal's tools"
	case terminalHostSlow:
		return "Marshal's terminal was too slow to start " + provider
	}
	return provider + " hit a " + plainInfraKind(kind)
}

// platformUnsupported is the failure kind of a worker this computer cannot
// run at all (governed workers need Linux amd64 confinement).
const platformUnsupported = "platform cannot run this worker"

func plainInfraKind(kind string) string {
	switch kind {
	case platformUnsupported:
		return "platform limit"
	case "provider quota or rate limit":
		return "usage limit"
	case "provider sign-in or credentials":
		return "sign-in problem"
	case "provider cooldown or failover refusal":
		return "provider problem"
	case "transport or network":
		return "network problem"
	case "harness context or tool output":
		return "tool output problem"
	case "worker process failure":
		return "crash"
	case workerLeaseExpired:
		return "the task's time ran out"
	case marshalToolsLost:
		return "lost connection to Marshal's tools"
	case terminalHostSlow:
		return "slow terminal"
	default:
		return "problem"
	}
}
