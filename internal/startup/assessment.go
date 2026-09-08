package startup

import (
	"sort"
	"time"
)

// Assessment is the complete startup picture: what was observed, what phase
// that puts MARSHAL in, and which capabilities are consequently available.
//
// It is produced before and independently of opening the execution runtime, so
// a runtime that cannot open becomes a described condition rather than a
// terminated process.
type Assessment struct {
	Phase Phase `json:"phase"`
	// Checks are every observation made, in stable order.
	Checks []Check `json:"checks"`
	// Capabilities are what MARSHAL can do given those observations.
	Capabilities []Capability `json:"capabilities"`
	// FirstRun reports that MARSHAL has not been set up here before.
	FirstRun bool `json:"first_run"`
	// ProjectPath is the project directory, when one was found.
	ProjectPath string `json:"project_path,omitempty"`
	// RecoveryAvailable reports that interrupted work was found. Nothing is
	// resumed on its own; the user is offered the choice.
	RecoveryAvailable bool `json:"recovery_available"`
	// AssessedAt is when the observations were made, so a reader can judge
	// how current they are.
	AssessedAt time.Time `json:"assessed_at"`
	// Duration is how long assessment took, for the performance budget.
	Duration time.Duration `json:"duration"`
}

// ControlCenterOpens reports whether the control center can be presented.
func (a Assessment) ControlCenterOpens() bool { return a.Phase.ControlCenterOpens() }

// ExecutionPermitted reports whether work may execute.
func (a Assessment) ExecutionPermitted() bool { return a.Phase.ExecutionPermitted() }

// Has reports whether a capability is available.
func (a Assessment) Has(capability Capability) bool {
	for _, available := range a.Capabilities {
		if available == capability {
			return true
		}
	}
	return false
}

// Check returns a named observation.
func (a Assessment) Check(id string) (Check, bool) {
	for _, check := range a.Checks {
		if check.ID == id {
			return check, true
		}
	}
	return Check{}, false
}

// Blocking returns the required checks that are not healthy, most severe
// first. These are the reasons execution is unavailable, and they are exactly
// what a user should be shown when they ask why.
func (a Assessment) Blocking() []Check {
	var blocking []Check
	for _, check := range a.Checks {
		if check.Blocking() {
			blocking = append(blocking, check)
		}
	}
	return blocking
}

// Attention returns checks a user should look at, including non-required ones
// that are broken or unknown. A missing optional tool is not listed here; a
// broken one is.
func (a Assessment) Attention() []Check {
	var attention []Check
	for _, check := range a.Checks {
		switch check.Status {
		case StatusReady, StatusLimited, StatusOptional, StatusMissing:
			if check.Status == StatusMissing && check.Required {
				attention = append(attention, check)
			}
			continue
		default:
			attention = append(attention, check)
		}
	}
	return attention
}

// Summarize builds the assessment from a set of observations.
//
// The phase follows from the checks rather than being asserted alongside them,
// so a caller cannot report a healthier phase than its own observations
// support. Capabilities are computed the same way: they start from everything
// MARSHAL can do and are removed by failing checks, which is what makes a
// missing optional tool disable one feature instead of the whole product.
func Summarize(checks []Check, options SummaryOptions) Assessment {
	assessment := Assessment{
		Checks:            append([]Check(nil), checks...),
		FirstRun:          options.FirstRun,
		ProjectPath:       options.ProjectPath,
		RecoveryAvailable: options.RecoveryAvailable,
		AssessedAt:        options.Now,
		Duration:          options.Duration,
	}
	if assessment.AssessedAt.IsZero() {
		assessment.AssessedAt = time.Now().UTC()
	}
	sort.SliceStable(assessment.Checks, func(a, b int) bool {
		return assessment.Checks[a].ID < assessment.Checks[b].ID
	})

	// The control and recovery surfaces are available unless the core failed.
	// They are added first and never removed by a later check, because they
	// are the surfaces used to repair whatever a later check found.
	available := map[Capability]bool{}
	for _, capability := range alwaysAvailable {
		available[capability] = true
	}
	for _, capability := range []Capability{
		CapProjectExecution, CapNetworkEgress, CapProviderExecution,
	} {
		available[capability] = true
	}
	// ULTRA is opt-in and evidence-bearing: it starts absent and is added only
	// by a check that verified an entitlement, never by default.
	available[CapUltra] = options.UltraEntitled

	coreFailed := false
	anyBlocking := false
	anyDegraded := false
	anyAttention := false

	for _, check := range assessment.Checks {
		healthy := check.Status.Healthy() || check.Status == StatusOptional
		if healthy {
			if check.Status == StatusLimited {
				anyDegraded = true
			}
			continue
		}
		// A failing check removes exactly the capabilities it declared, and
		// never one of the protected control surfaces.
		for _, capability := range check.Capabilities {
			if !IsAlwaysAvailable(capability) {
				available[capability] = false
			}
		}
		if check.Dimension == DimensionCore && CoreFatal(check.Reason) {
			coreFailed = true
		}
		if check.Blocking() {
			anyBlocking = true
		} else {
			anyAttention = true
		}
	}

	for _, capability := range alwaysAvailable {
		available[capability] = !coreFailed
	}

	assessment.Capabilities = make([]Capability, 0, len(available))
	for capability, present := range available {
		if present {
			assessment.Capabilities = append(assessment.Capabilities, capability)
		}
	}
	sort.Slice(assessment.Capabilities, func(a, b int) bool {
		return assessment.Capabilities[a] < assessment.Capabilities[b]
	})

	switch {
	case coreFailed:
		assessment.Phase = PhaseCoreFailed
	case anyBlocking:
		assessment.Phase = PhaseExecutionBlocked
	case assessment.RecoveryAvailable:
		assessment.Phase = PhaseRecoveryAvailable
	case anyAttention:
		assessment.Phase = PhaseNeedsAttention
	case anyDegraded:
		assessment.Phase = PhaseLimited
	default:
		assessment.Phase = PhaseReady
	}
	return assessment
}

// SummaryOptions carries the context that is observed rather than checked.
type SummaryOptions struct {
	FirstRun          bool
	ProjectPath       string
	RecoveryAvailable bool
	// UltraEntitled must come from a verified entitlement. A caller passing
	// true without one is asserting something Process 00 will refuse at the
	// gate; startup does not grant ULTRA, it only reports it.
	UltraEntitled bool
	Now           time.Time
	Duration      time.Duration
}

// UserSafe reports whether every check summary is fit to display, and returns
// the offending IDs when not. Surfaces use this to avoid rendering raw
// internal errors; tests use it to prove none can reach a user.
func (a Assessment) UserSafe() (bool, []string) {
	var unsafe []string
	for _, check := range a.Checks {
		if !check.userSafe() {
			unsafe = append(unsafe, check.ID)
		}
	}
	return len(unsafe) == 0, unsafe
}
