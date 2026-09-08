package startup

import (
	"fmt"
	"sort"
	"strings"
)

// This file renders the control center's landing view.
//
// The rendering rule is that every line comes from an observation. There is no
// path by which a surface can print "Ready" without a check having returned
// READY, which is what keeps the screen honest when the environment is not.

// Action is something the user can do from the landing screen.
type Action struct {
	// Key is the stable identifier a surface binds to.
	Key string `json:"key"`
	// Label is what the user reads.
	Label string `json:"label"`
	// Available reports whether the action can be taken now.
	Available bool `json:"available"`
	// Unavailable explains why not, in the user's terms. It is set whenever
	// Available is false, so an action is never simply greyed out with no
	// account of what would enable it.
	Unavailable string `json:"unavailable,omitempty"`
	// Capability is the capability the action requires, if any.
	Capability Capability `json:"capability,omitempty"`
}

// Landing is the control center's opening view.
type Landing struct {
	// Headline is the one-line state of the system.
	Headline string `json:"headline"`
	// Detail expands on the headline without internals.
	Detail string `json:"detail,omitempty"`
	// Phase is the machine-readable state behind the headline.
	Phase Phase `json:"phase"`
	// Actions are the offered choices, in a stable order.
	Actions []Action `json:"actions"`
	// Attention lists what the user should look at.
	Attention []Check `json:"attention,omitempty"`
	// ProjectPath is the open project, when there is one.
	ProjectPath string `json:"project_path,omitempty"`
	// FirstRun drives the recommendation to run Setup.
	FirstRun bool `json:"first_run"`
}

// landingActions is the fixed action set, in presentation order. Availability
// is decided per render from the assessment; the list itself is constant so
// that an action never silently disappears — a user who cannot find "Doctor"
// on a broken system has lost the thing they needed most.
var landingActions = []struct {
	key        string
	label      string
	capability Capability
}{
	{"open-project", "Start or open a project", CapProjectExecution},
	{"resume", "Resume previous work", CapRecovery},
	{"setup", "Set up MARSHAL", CapSetup},
	{"doctor", "Run Doctor", CapDoctor},
	{"recent", "Recent projects", CapHistory},
	{"help", "Help", CapHelp},
	{"exit", "Exit", ""},
}

// BuildLanding renders the landing view from an assessment.
func BuildLanding(assessment Assessment) Landing {
	landing := Landing{
		Phase:       assessment.Phase,
		ProjectPath: assessment.ProjectPath,
		FirstRun:    assessment.FirstRun,
		Attention:   assessment.Attention(),
	}
	landing.Headline, landing.Detail = headlineFor(assessment)

	for _, spec := range landingActions {
		action := Action{Key: spec.key, Label: spec.label, Capability: spec.capability}
		switch {
		case spec.capability == "":
			action.Available = true
		case spec.key == "resume":
			// Resume is offered only when there is something to resume. An
			// action that does nothing is worse than an absent one.
			action.Available = assessment.RecoveryAvailable && assessment.Has(CapRecovery)
			if !action.Available {
				action.Unavailable = "There is no interrupted work to resume."
			}
		default:
			action.Available = assessment.Has(spec.capability)
			if !action.Available {
				action.Unavailable = unavailableReason(assessment, spec.capability)
			}
		}
		landing.Actions = append(landing.Actions, action)
	}
	return landing
}

// unavailableReason explains a disabled action using the check that disabled
// it, so the explanation names the actual cause rather than a generic message.
func unavailableReason(assessment Assessment, capability Capability) string {
	var causes []string
	for _, check := range assessment.Checks {
		if check.Status.Healthy() || check.Status == StatusOptional {
			continue
		}
		for _, gated := range check.Capabilities {
			if gated == capability {
				causes = append(causes, check.Summary)
			}
		}
	}
	if len(causes) == 0 {
		return "This is not available yet."
	}
	sort.Strings(causes)
	return strings.Join(causes, " ")
}

func headlineFor(assessment Assessment) (string, string) {
	switch assessment.Phase {
	case PhaseReady:
		if assessment.FirstRun {
			return "MARSHAL is ready.", "This project has not been set up yet. Setup is recommended but not required to look around."
		}
		return "MARSHAL is ready.", ""
	case PhaseLimited:
		return "MARSHAL is ready, with limitations.", describeAffected(assessment)
	case PhaseNeedsAttention:
		return "MARSHAL is running. Some things need attention.", describeAffected(assessment)
	case PhaseRecoveryAvailable:
		// The headline leads with the pending decision rather than with
		// readiness. The environment is fine, but saying "ready" first invites
		// the user to carry on and discover the interrupted work later, which
		// is the moment it is most likely to be resumed by accident.
		return "Earlier work was interrupted.",
			"MARSHAL is otherwise ready. Review the interrupted work and choose whether to resume it. Nothing has been resumed automatically."
	case PhaseExecutionBlocked:
		// The wording matters: the user is told what still works before what
		// does not, because the screen they are reading is the thing that
		// still works.
		return "MARSHAL is open. Work cannot run yet.", describeAffected(assessment)
	case PhaseCoreFailed:
		return "MARSHAL cannot start here.", describeAffected(assessment)
	default:
		return "Starting MARSHAL.", ""
	}
}

// describeAffected summarises the blocking and attention items in prose, using
// only the checks' own user-safe summaries.
func describeAffected(assessment Assessment) string {
	seen := map[string]bool{}
	var parts []string
	for _, check := range append(assessment.Blocking(), assessment.Attention()...) {
		if seen[check.ID] || strings.TrimSpace(check.Summary) == "" {
			continue
		}
		seen[check.ID] = true
		parts = append(parts, check.Summary)
	}
	sort.Strings(parts)
	if len(parts) > 3 {
		remaining := len(parts) - 3
		parts = append(parts[:3], fmt.Sprintf("%d more item(s) are listed below.", remaining))
	}
	return strings.Join(parts, " ")
}

// Render produces the plain-text landing screen.
//
// It reads only from the Landing it is given, so a caller cannot inject a
// status the assessment did not produce.
func (l Landing) Render() string {
	var b strings.Builder
	b.WriteString(l.Headline)
	b.WriteString("\n")
	if l.Detail != "" {
		b.WriteString(l.Detail)
		b.WriteString("\n")
	}
	if l.ProjectPath != "" {
		fmt.Fprintf(&b, "\nProject: %s\n", l.ProjectPath)
	}

	b.WriteString("\nWhat would you like to do?\n")
	for _, action := range l.Actions {
		if action.Available {
			fmt.Fprintf(&b, "  %-28s %s\n", action.Key, action.Label)
			continue
		}
		fmt.Fprintf(&b, "  %-28s %s — unavailable: %s\n", action.Key, action.Label, action.Unavailable)
	}

	if len(l.Attention) > 0 {
		b.WriteString("\nNeeds attention:\n")
		for _, check := range l.Attention {
			fmt.Fprintf(&b, "  [%s] %s\n", check.Status, check.Summary)
			if check.Impact != "" {
				fmt.Fprintf(&b, "      Impact: %s\n", check.Impact)
			}
			if check.Remedy != "" {
				fmt.Fprintf(&b, "      Next:   %s\n", check.Remedy)
			}
		}
	}
	return b.String()
}
