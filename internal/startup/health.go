// Package startup implements MARSHAL Process 01: entry into the control
// center.
//
// The governing product rule is that starting MARSHAL is entry into a control
// center, not an environment prerequisite test. If the core can run, the
// control center opens — even when Git, tooling, harnesses, providers, the
// sandbox or the network are missing or degraded. Those failures reduce what
// can be *executed*; they do not remove the surfaces a user needs in order to
// understand and fix them. A tool that refuses to start until its environment
// is already correct is least available exactly when it is most needed.
//
// Process 01 is subordinate to Process 00 (internal/constitution). Startup may
// observe, classify, explain and recommend. It may not mark anything Ready
// that it did not verify, authorize a repair, enable ULTRA, weaken sandbox or
// network policy, or declare provider qualification.
package startup

import "strings"

// Phase is the startup state machine. It advances only on observed facts.
type Phase string

const (
	// PhaseBooting is the initial state before anything has been observed.
	PhaseBooting Phase = "BOOTING"
	// PhaseControlCenterReady means the control center can open. It says
	// nothing about whether work can execute.
	PhaseControlCenterReady Phase = "CONTROL_CENTER_READY"
	// PhaseReady means the control center is open and execution prerequisites
	// are satisfied.
	PhaseReady Phase = "READY"
	// PhaseLimited means the control center is open and some capabilities are
	// unavailable, with the affected ones named.
	PhaseLimited Phase = "LIMITED"
	// PhaseNeedsAttention means something is wrong that the user should fix,
	// while the control center remains fully usable.
	PhaseNeedsAttention Phase = "NEEDS_ATTENTION"
	// PhaseExecutionBlocked means work cannot execute safely. The control
	// center, Setup, Doctor, Help, inspection and recovery all stay open: this
	// is the state in which those surfaces matter most.
	PhaseExecutionBlocked Phase = "EXECUTION_BLOCKED"
	// PhaseRecoveryAvailable means interrupted work was found and recovery is
	// offered. Nothing is resumed without the user asking.
	PhaseRecoveryAvailable Phase = "RECOVERY_AVAILABLE"
	// PhaseCoreFailed is the only state in which the control center cannot
	// open. It is reserved for failures of MARSHAL itself, and is deliberately
	// hard to reach.
	PhaseCoreFailed Phase = "CORE_FAILED"
)

// ControlCenterOpens reports whether the control center can be presented in
// this phase. Every phase except a genuine core failure opens it.
func (p Phase) ControlCenterOpens() bool {
	return p != PhaseCoreFailed && p != PhaseBooting
}

// ExecutionPermitted reports whether work may execute in this phase.
//
// READY and LIMITED both permit it. LIMITED means some optional capability is
// unavailable on this machine — no policy-enforced egress, say — and refusing
// to run local work for that reason would punish the user for a limitation
// that does not affect what they are doing. Which specific capabilities are
// available is answered by Assessment.Has, not by the phase.
//
// Every other phase requires something to change first. Execution authority
// itself remains with Process 00 and the runtime; this is a startup
// precondition, not a grant.
func (p Phase) ExecutionPermitted() bool {
	return p == PhaseReady || p == PhaseLimited
}

// Status is the human-facing state of one readiness check. The set is small on
// purpose: a user reading a startup screen needs to know whether something
// works, whether it half-works, or whether nobody checked.
type Status string

const (
	// StatusReady means verified working.
	StatusReady Status = "READY"
	// StatusLimited means working with a named restriction.
	StatusLimited Status = "LIMITED"
	// StatusNeedsAttention means present but misconfigured or unhealthy.
	StatusNeedsAttention Status = "NEEDS_ATTENTION"
	// StatusMissing means absent. For an optional check this is normal.
	StatusMissing Status = "MISSING"
	// StatusBroken means present but unusable.
	StatusBroken Status = "BROKEN"
	// StatusOptional means absent and not required for anything the user has
	// asked for.
	StatusOptional Status = "OPTIONAL"
	// StatusUnknown means it was not checked, or the check itself failed.
	//
	// UNKNOWN is never Ready. This is the single most load-bearing rule in the
	// health model: a check that could not run tells you nothing, and treating
	// silence as success is how a startup screen ends up lying.
	StatusUnknown Status = "UNKNOWN"
)

// Healthy reports whether the status may be presented as working. Only READY
// and LIMITED qualify, and LIMITED must carry its restriction.
func (s Status) Healthy() bool { return s == StatusReady || s == StatusLimited }

// Blocking reports whether this status should stop execution when the check is
// required. UNKNOWN blocks: not having looked is not evidence of health.
func (s Status) Blocking() bool {
	switch s {
	case StatusReady, StatusLimited, StatusOptional:
		return false
	default:
		return true
	}
}

// Dimension separates the three questions a startup screen must answer
// independently. Collapsing them is what produces the failure this package
// exists to fix: a missing optional tool becoming a refusal to start.
type Dimension string

const (
	// DimensionCore asks whether MARSHAL itself can run its control center and
	// canonical state. Only this dimension can prevent startup.
	DimensionCore Dimension = "core"
	// DimensionEnvironment asks about tooling, harnesses and security
	// prerequisites.
	DimensionEnvironment Dimension = "environment"
	// DimensionProject asks about the project in the current directory.
	DimensionProject Dimension = "project"
	// DimensionExecution asks whether work can run under policy right now.
	DimensionExecution Dimension = "execution"
)

// Check is one readiness observation.
type Check struct {
	// ID is the stable machine name, used by tests and by every surface.
	ID string `json:"id"`
	// Dimension places the check in the health model.
	Dimension Dimension `json:"dimension"`
	// Status is the observed state.
	Status Status `json:"status"`
	// Required marks a check that execution genuinely depends on. A failing
	// required check blocks execution; it never blocks the control center.
	Required bool `json:"required"`
	// Summary is one user-facing line. It never contains raw internal errors,
	// stack traces, subprocess stderr, paths outside the project or secrets.
	Summary string `json:"summary"`
	// Impact says what is unavailable as a result, in the user's terms.
	Impact string `json:"impact,omitempty"`
	// Remedy says what would fix it. It is advice, never an action taken.
	Remedy string `json:"remedy,omitempty"`
	// Reason is the stable machine code for this outcome.
	Reason ReasonCode `json:"reason"`
	// Capabilities names the capabilities this check gates. When it fails,
	// exactly these are disabled and nothing else.
	Capabilities []Capability `json:"capabilities,omitempty"`
	// Detail is operator-facing context. It is shown under Details, never in
	// the summary line, and is still sanitized of secrets.
	Detail string `json:"detail,omitempty"`
}

// Capability is a thing MARSHAL can do. Degradation is expressed by removing
// capabilities, which is what keeps a missing optional tool from turning into
// a refusal to start.
type Capability string

const (
	// CapControlCenter is the control center itself. It is removed only on a
	// core failure.
	CapControlCenter Capability = "control-center"
	// CapSetup, CapDoctor, CapHelp, CapInspect, CapHistory and CapRecovery are
	// the surfaces a user needs in order to diagnose and repair. They are
	// deliberately never gated on environment or project readiness, because
	// they are the tools for fixing exactly those problems.
	CapSetup    Capability = "setup"
	CapDoctor   Capability = "doctor"
	CapHelp     Capability = "help"
	CapInspect  Capability = "inspect"
	CapHistory  Capability = "history"
	CapRecovery Capability = "recovery"

	// CapProjectExecution is running work against a project.
	CapProjectExecution Capability = "project-execution"
	// CapNetworkEgress is policy-enforced outbound access.
	CapNetworkEgress Capability = "network-egress"
	// CapProviderExecution is running a provider through MARSHAL.
	CapProviderExecution Capability = "provider-execution"
	// CapUltra is ULTRA operation. It is never present without a valid
	// entitlement.
	CapUltra Capability = "ultra"
)

// alwaysAvailable are the capabilities that survive every failure short of a
// core failure. This list is the mechanical form of the pack's rule that
// safety-critical failures may block execution but must not block the
// surfaces used to understand and repair them.
var alwaysAvailable = []Capability{
	CapControlCenter, CapSetup, CapDoctor, CapHelp,
	CapInspect, CapHistory, CapRecovery,
}

// AlwaysAvailable returns the capabilities that never depend on environment or
// project readiness.
func AlwaysAvailable() []Capability {
	out := make([]Capability, len(alwaysAvailable))
	copy(out, alwaysAvailable)
	return out
}

// IsAlwaysAvailable reports whether a capability is one of the protected
// control and recovery surfaces.
func IsAlwaysAvailable(capability Capability) bool {
	for _, protected := range alwaysAvailable {
		if protected == capability {
			return true
		}
	}
	return false
}

// Blocking reports whether this check should stop execution: a required check
// that is not healthy.
func (c Check) Blocking() bool { return c.Required && c.Status.Blocking() }

// internalErrorMarkers are the shapes raw internal errors take when they leak
// into a user-facing string: subprocess stderr, database driver messages, Go
// runtime output, and quoted SQL or command lines.
//
// The list is a backstop, not the mechanism. Summaries are written as prose by
// the probes themselves, and this check exists so that a future probe which
// forwards an error verbatim fails a test rather than shipping the error to a
// user. It is matched case-insensitively.
var internalErrorMarkers = []string{
	"exit status", "goroutine", "panic:", "stack trace", "runtime error",
	"sqlite", "pragma", "fatal:", "no such file or directory",
	"unable to open", "invalid memory address", "nil pointer",
	"0x", "\t", "\n",
}

// userSafe reports whether the summary is fit to show a user.
func (c Check) userSafe() bool {
	if strings.TrimSpace(c.Summary) == "" {
		return false
	}
	lowered := strings.ToLower(c.Summary)
	for _, marker := range internalErrorMarkers {
		if strings.Contains(lowered, marker) {
			return false
		}
	}
	return true
}
