package app

import "strings"

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
	{"provider quota or rate limit", []string{"usage limit", "rate limit", "rate-limit", "quota", "hit your limit", "5-hour limit", "too many requests", "resource_exhausted", "out of credits", "insufficient credits"}},
	{"provider sign-in or credentials", []string{"sign-in", "signin", "credential", "unauthorized", "authentication", "not logged in", "login required", "access token", "401"}},
	{"provider cooldown or failover refusal", []string{"provider cooldown", "provider failover", "not limited by its provider", "provider execution failure"}},
	{"transport or network", []string{"connection refused", "connection reset", "broken pipe", "eof", "timeout", "timed out", "deadline exceeded", "tls", "dns", "network", "socket", "transport", "502", "503", "504", "overloaded", "unavailable"}},
	{"harness context or tool output", []string{"context budget", "context window", "context length", "skills context", "malformed", "invalid json", "unexpected end of json", "parse", "output invalid", "stream"}},
}

// classifyInfraFailure names the kind of failure that ended a worker attempt
// before it delivered a hand-in, with a bounded reason. Such an attempt is
// never a rejection of delivered work, so it is not rework; an unrecognised
// failure is reported as a worker process failure.
func classifyInfraFailure(text string) (kind, reason string) {
	reason = strings.Join(strings.Fields(text), " ")
	if len(reason) > 300 {
		reason = reason[:300]
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
	default:
		return "problem"
	}
}
