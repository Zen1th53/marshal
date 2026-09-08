package plan

import (
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/model"
)

// This file assigns a harness, model and native configuration to each role.
//
// The selection order is fixed and governance comes first. That ordering is
// the point: a provider MARSHAL cannot govern is ineligible however fast,
// cheap or capable it is, and it stays ineligible as a fallback — a fallback
// that escapes governance would turn every outage into a bypass.
//
// Nothing here invents. Model names, flags and capabilities come from a probed
// harness profile or they are not used, because a plausible-sounding flag that
// does not exist fails at execution time, when it is expensive, rather than
// here, when it is free.

// Eligibility is why a candidate was or was not usable, in selection order.
//
// The tiers are numbered to match the specification's priority list, so a
// rejection can be traced to the rule that produced it rather than to an
// opaque score.
type Eligibility int

const (
	// EligibleGovernance is tier 1: MARSHAL must be able to govern it.
	EligibleGovernance Eligibility = iota + 1
	// EligibleCapability is tier 2: required capabilities must be present.
	EligibleCapability
	// EligibleFit is tier 3: the harness must suit the task and role.
	EligibleFit
	// EligibleIndependence is tier 4: a verifier must not be the thing it verifies.
	EligibleIndependence
	// EligibleHealth is tier 5: the profile must describe the installed build.
	EligibleHealth
	// EligibleContext is tier 6: the work must fit the context window.
	EligibleContext
	// EligibleCapacity is tier 7: known-zero quota disqualifies; UNKNOWN does not.
	EligibleCapacity
)

// String names the tier in operator-facing terms.
func (e Eligibility) String() string {
	switch e {
	case EligibleGovernance:
		return "governance"
	case EligibleCapability:
		return "capabilities"
	case EligibleFit:
		return "task fit"
	case EligibleIndependence:
		return "verification independence"
	case EligibleHealth:
		return "harness health"
	case EligibleContext:
		return "context fit"
	case EligibleCapacity:
		return "capacity"
	default:
		return "unknown"
	}
}

// NativeConfig is the harness configuration a role will run under.
//
// Every field is either evidenced by the probed profile or left empty. An
// empty field means "not configured", never "configured to the default we
// assumed" — the difference matters when a later step asks why a setting was
// chosen and the honest answer is that it was not.
type NativeConfig struct {
	Model string `json:"model,omitempty"`
	// Effort is the reasoning knob, when the harness has one.
	Effort string `json:"effort,omitempty"`
	// ApprovalMode is how the harness asks before acting. MARSHAL never sets
	// this to anything that bypasses approvals; such a request is refused
	// rather than translated into the nearest permissive setting.
	ApprovalMode string `json:"approval_mode,omitempty"`
	SandboxMode  string `json:"sandbox_mode,omitempty"`
	// ToolPolicy names the tool access the role gets.
	ToolPolicy string `json:"tool_policy,omitempty"`
	// Subagents reports whether the harness may spawn its own workers.
	Subagents bool `json:"subagents,omitempty"`
	// Headless reports non-interactive operation.
	Headless bool `json:"headless,omitempty"`
	// OutputFormat is the response shape requested from the harness.
	OutputFormat string `json:"output_format,omitempty"`
	// TimeoutSeconds bounds a single invocation. Zero means unset.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// Omitted names settings that were wanted but are not supported by this
	// harness version. Recording them keeps the omission visible rather than
	// letting a silently dropped setting look like a choice.
	Omitted []string `json:"omitted,omitempty"`
}

// Assignment is one role's harness, model and configuration.
type Assignment struct {
	Role Role `json:"role"`
	// Tasks are the tasks this assignment covers.
	Tasks []string `json:"tasks,omitempty"`

	Harness        string `json:"harness"`
	HarnessVersion string `json:"harness_version"`
	Provider       string `json:"provider"`
	Model          string `json:"model,omitempty"`

	Native NativeConfig `json:"native"`

	Governance constitution.GovernanceState `json:"governance"`
	// Capacity is what is known about remaining quota. UNKNOWN is a valid and
	// common answer, and is never rendered as a number.
	Capacity goalintake.Capacity `json:"capacity"`
	// Reason explains the selection in the user's terms.
	Reason string `json:"reason"`
	// Fallback is the governed alternative if this assignment fails.
	Fallback *Fallback `json:"fallback,omitempty"`
	// ProbedAt is when the harness evidence was gathered. A stale probe is a
	// weaker claim than a fresh one, and the timestamp is what lets a reader
	// tell the difference.
	ProbedAt time.Time `json:"probed_at"`
	// Independent marks an assignment that must not review its own work.
	Independent bool `json:"independent,omitempty"`
	// SharesWorkerHarness records that an independent role ended up on the
	// same harness as the work it checks, because no separate governed
	// harness was available. The check is still worth doing; it is simply a
	// weaker claim, and hiding that would overstate the verification.
	SharesWorkerHarness bool `json:"shares_worker_harness,omitempty"`
}

// Fallback is the governed alternative for an assignment.
//
// A fallback is a full assignment rather than a provider name because
// switching provider changes the model, the configuration and possibly the
// context strategy. It also carries what must be re-established after the
// switch: a fallback cannot inherit the original's approvals or independence
// by being called a fallback.
type Fallback struct {
	Harness  string `json:"harness"`
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	Reason   string `json:"reason"`
	// Revalidate names what must be re-checked after switching to this.
	Revalidate []string `json:"revalidate,omitempty"`
}

// Rejection records a candidate that was not used and the tier that stopped it.
type Rejection struct {
	Harness string      `json:"harness"`
	Tier    Eligibility `json:"tier"`
	Reason  string      `json:"reason"`
}

// AssignmentPlan is the full set of assignments plus what was rejected.
type AssignmentPlan struct {
	Assignments []Assignment `json:"assignments"`
	Rejected    []Rejection  `json:"rejected,omitempty"`
	// Unresolved names roles nothing eligible could be found for. A role here
	// blocks the plan: a team member with no way to do the work is not a plan,
	// and discovering that at execution time is worse than refusing now.
	Unresolved []string `json:"unresolved,omitempty"`
}

// HarnessCandidate is one available harness with its probed evidence.
type HarnessCandidate struct {
	Profile model.HarnessProfile
	// InstalledVersion is what is actually on the machine right now. It is
	// separate from the profile's version so drift can be detected: a profile
	// describing a different build is evidence about that build, not this one.
	InstalledVersion string
	Provider         string
	Capacity         goalintake.Capacity
	// ContextTokens is the usable context window, when known. Zero means
	// unknown, which is not treated as zero.
	ContextTokens int
}

// AssignRequest is the input to harness and model assignment.
type AssignRequest struct {
	Team       Team
	Tasks      []Task
	Assessment goalintake.Assessment
	Candidates []HarnessCandidate
	// RequiredCapabilities are the harness features the work needs.
	RequiredCapabilities []string
	// EstimatedContextTokens is the context the work is expected to need.
	// Zero means unestimated, which does not disqualify anything.
	EstimatedContextTokens int
	Now                    time.Time
}

// AssignHarnesses selects a harness, model and native configuration per role.
//
// Candidates are filtered through the tiers in order and the first surviving
// candidate wins, rather than a weighted score being computed. Scoring would
// let a very fast ungovernable provider outrank a governed one, which is
// exactly the trade the priority order exists to forbid.
func AssignHarnesses(request AssignRequest) AssignmentPlan {
	now := timeOrNow(request.Now)
	assignmentPlan := AssignmentPlan{}

	// usedByWorkers records the harnesses already assigned to producing roles,
	// so a checking role can prefer one that is not among them.
	usedByWorkers := map[string]bool{}

	tasksByRole := tasksForRoles(request.Team, request.Tasks)

	// Producing roles are assigned first so that when a checking role is
	// assigned, there is something concrete for independence to be measured
	// against. Assigning in team order would make independence depend on the
	// order roles happen to appear in.
	for _, member := range orderedForAssignment(request.Team) {
		eligible, rejections := filterCandidates(request, member, now, usedByWorkers)
		assignmentPlan.Rejected = append(assignmentPlan.Rejected, rejections...)

		if len(eligible) == 0 {
			assignmentPlan.Unresolved = append(assignmentPlan.Unresolved,
				string(member.Role)+": no governed harness is available for this role")
			continue
		}

		chosen := eligible[0]
		assignment := Assignment{
			Role:           member.Role,
			Tasks:          tasksByRole[member.Role],
			Harness:        chosen.Profile.Harness,
			HarnessVersion: chosen.InstalledVersion,
			Provider:       chosen.Provider,
			Model:          modelFor(chosen),
			Native:         nativeConfigFor(chosen, member, request.Assessment),
			Governance:     harness.AssessGovernance(chosen.Profile, chosen.InstalledVersion, now).State,
			Capacity:       chosen.Capacity,
			Reason:         selectionReason(chosen, member),
			ProbedAt:       chosen.Profile.ProbedAt,
			Independent:    member.Independent,
		}
		if len(eligible) > 1 {
			assignment.Fallback = fallbackFor(eligible[1], member)
		}
		// A check performed on the same harness as the work is still worth
		// doing, but it is a weaker claim, and saying so is what stops it
		// being read as full independence later.
		if member.Independent && usedByWorkers[chosen.Profile.Harness] {
			assignment.SharesWorkerHarness = true
			assignment.Reason += " No separate governed harness was available, so this check runs on the same harness as the work."
		}
		if !member.Independent {
			usedByWorkers[chosen.Profile.Harness] = true
		}
		assignmentPlan.Assignments = append(assignmentPlan.Assignments, assignment)
	}

	sort.SliceStable(assignmentPlan.Assignments, func(a, b int) bool {
		return assignmentPlan.Assignments[a].Role < assignmentPlan.Assignments[b].Role
	})
	sort.Strings(assignmentPlan.Unresolved)
	return assignmentPlan
}

// Complete reports whether every role has a harness.
func (a AssignmentPlan) Complete() bool { return len(a.Unresolved) == 0 }

// For returns the assignment for a role.
func (a AssignmentPlan) For(role Role) (Assignment, bool) {
	for _, assignment := range a.Assignments {
		if assignment.Role == role {
			return assignment, true
		}
	}
	return Assignment{}, false
}

// filterCandidates applies the priority tiers in order.
//
// Each tier removes candidates; the survivors keep their relative order, so
// the caller's preference breaks ties rather than an invented ranking.
func filterCandidates(request AssignRequest, member Member, now time.Time, usedByWorkers map[string]bool) ([]HarnessCandidate, []Rejection) {
	var surviving []HarnessCandidate
	var rejections []Rejection

	reject := func(candidate HarnessCandidate, tier Eligibility, reason string) {
		rejections = append(rejections, Rejection{
			Harness: candidate.Profile.Harness, Tier: tier, Reason: reason,
		})
	}

	for _, candidate := range request.Candidates {
		// Tier 1: governance. An ungovernable harness is ineligible outright,
		// which is what keeps it out of the fallback list too.
		governance := harness.AssessGovernance(candidate.Profile, candidate.InstalledVersion, now)
		if !governance.Governed() {
			reason := "MARSHAL cannot govern this harness."
			if len(governance.Reasons) > 0 {
				reason = governance.Reasons[0]
			}
			reject(candidate, EligibleGovernance, reason)
			continue
		}

		// Tier 2: required capabilities must be genuinely present. A feature
		// needing a probe is not a feature yet.
		intelligence := harness.NewIntelligence()
		missing := ""
		for _, capability := range request.RequiredCapabilities {
			status := intelligence.AuditKnob(candidate.Profile, capability)
			if status != model.StatusNative && status != model.StatusEmulated {
				missing = capability
				break
			}
		}
		if missing != "" {
			reject(candidate, EligibleCapability,
				"This harness does not support "+missing+".")
			continue
		}

		// Tier 3: task and role fit. A harness with no model it can run is not
		// a fit however well it is governed.
		if modelFor(candidate) == "" {
			reject(candidate, EligibleFit, "This harness has no usable model.")
			continue
		}

		// Tier 5: health.
		//
		// Version drift is deliberately not re-checked here: AssessGovernance
		// already treats a probe describing a different build as DEGRADED, so
		// tier 1 has rejected it before this point. Repeating the check would
		// be dead code that reads like a safeguard, which is worse than no
		// check at all — a reader would trust it.
		//
		// Freshness is checked, because a profile can be current for the
		// installed version and still be too old to rely on.
		if !candidate.Profile.IsFresh(now) {
			reject(candidate, EligibleHealth,
				"The evidence for this harness is out of date and needs re-probing.")
			continue
		}

		// Tier 6: context fit, only when both sides are known. An unknown
		// window does not disqualify: refusing on an unmeasured number would
		// be inventing the measurement.
		if request.EstimatedContextTokens > 0 && candidate.ContextTokens > 0 &&
			request.EstimatedContextTokens > candidate.ContextTokens {
			reject(candidate, EligibleContext,
				"The work does not fit this harness's context window.")
			continue
		}

		// Tier 7: capacity. Known-zero disqualifies; UNKNOWN does not, because
		// treating unknown as empty would strand work on a guess.
		if candidate.Capacity.Exhausted() {
			reject(candidate, EligibleCapacity, "This provider has no remaining quota.")
			continue
		}

		surviving = append(surviving, candidate)
	}

	// Tier 4: independence. A verifier that runs on the same harness as the
	// work it checks shares that harness's blind spots, so an independent role
	// prefers a harness the workers are not on.
	//
	// This is a preference rather than a filter. Refusing to verify at all
	// because only one harness is governed would be worse than verifying on a
	// shared one — but the sharing is recorded on the assignment rather than
	// passed over, so nobody later reads that check as more independent than
	// it was.
	if member.Independent && len(surviving) > 1 && len(usedByWorkers) > 0 {
		sort.SliceStable(surviving, func(a, b int) bool {
			return !usedByWorkers[surviving[a].Profile.Harness] &&
				usedByWorkers[surviving[b].Profile.Harness]
		})
	}

	return surviving, rejections
}

// modelFor returns the model the harness will run, from evidence only.
//
// The default model is preferred, then the first supported model. Nothing is
// synthesised: an empty result means the profile does not say, and the caller
// treats that as a reason not to use the harness rather than as licence to
// guess a name.
func modelFor(candidate HarnessCandidate) string {
	if strings.TrimSpace(candidate.Profile.DefaultModel) != "" {
		return candidate.Profile.DefaultModel
	}
	if len(candidate.Profile.SupportedModels) > 0 {
		return candidate.Profile.SupportedModels[0]
	}
	return ""
}

// nativeConfigFor plans the harness configuration.
//
// Every setting is audited against the probed profile first. A setting the
// harness does not support is recorded as omitted rather than passed anyway,
// because a rejected flag at execution time is a failure the user has to
// diagnose, while an omission recorded here is something they can read.
func nativeConfigFor(candidate HarnessCandidate, member Member, assessment goalintake.Assessment) NativeConfig {
	intelligence := harness.NewIntelligence()
	config := NativeConfig{Model: modelFor(candidate)}

	want := func(knob, value string, assign func(string)) {
		if strings.TrimSpace(value) == "" {
			return
		}
		switch intelligence.AuditKnob(candidate.Profile, knob) {
		case model.StatusNative, model.StatusEmulated:
			assign(value)
		default:
			config.Omitted = append(config.Omitted, knob)
		}
	}

	// Harder or less reversible work is worth more reasoning, where the
	// harness has a knob for it.
	effort := "medium"
	if assessment.Complexity.AtLeast(goalintake.LevelHigh) ||
		assessment.Reversibility.AtLeast(goalintake.LevelMed) {
		effort = "high"
	}
	want("effort", effort, func(v string) { config.Effort = v })

	// Approvals stay on. MARSHAL never asks a harness to skip its own
	// confirmations: that would move an approval decision inside a component
	// MARSHAL is supposed to be governing.
	want("approval_mode", "prompt", func(v string) { config.ApprovalMode = v })
	want("sandbox_mode", "workspace", func(v string) { config.SandboxMode = v })
	want("output_format", "json", func(v string) { config.OutputFormat = v })

	// Native subagents are only planned where the work is genuinely parallel
	// and the role coordinates. Elsewhere they add an actor MARSHAL did not
	// choose and cannot see.
	if member.Role == RoleOrchestrator {
		if intelligence.AuditKnob(candidate.Profile, "subagents") == model.StatusNative {
			config.Subagents = true
		} else {
			config.Omitted = append(config.Omitted, "subagents")
		}
	}

	config.ToolPolicy = "least-privilege"
	config.Headless = true
	config.TimeoutSeconds = 600

	sort.Strings(config.Omitted)
	return config
}

// selectionReason states why this harness was chosen, in the user's terms.
func selectionReason(candidate HarnessCandidate, member Member) string {
	reason := "MARSHAL can govern " + candidate.Profile.Harness +
		" and its capabilities are evidenced by a current probe."
	if member.Independent {
		reason += " It is assigned to " + string(member.Role) +
			", which checks work rather than producing it."
	}
	// Capacity is described by the type that owns it, so the three states
	// (unknown, known zero, known positive) are worded one way across MARSHAL
	// and no number is invented here.
	if described := strings.TrimSpace(candidate.Capacity.Describe()); described != "" {
		reason += " " + described
	}
	return reason
}

// fallbackFor builds the governed alternative.
//
// Everything in Revalidate is something the fallback does not inherit.
// Approvals in particular are bound to what was approved, and switching the
// provider changes that, so an approval must be sought again rather than
// carried across.
func fallbackFor(candidate HarnessCandidate, member Member) *Fallback {
	fallback := &Fallback{
		Harness:  candidate.Profile.Harness,
		Provider: candidate.Provider,
		Model:    modelFor(candidate),
		Reason:   "Governed alternative if the primary harness becomes unavailable.",
		Revalidate: []string{
			"capability support on the replacement harness",
			"context fit for the replacement's window",
			"hard approvals, which do not transfer between providers",
		},
	}
	if member.Independent {
		fallback.Revalidate = append(fallback.Revalidate,
			"verification independence, which the replacement must still satisfy")
	}
	sort.Strings(fallback.Revalidate)
	return fallback
}

// orderedForAssignment puts producing roles before checking roles.
//
// Independence is measured against what the workers were actually given, so
// the workers have to be assigned first. Within each group the team's own
// order is kept, so the result stays deterministic.
func orderedForAssignment(team Team) []Member {
	ordered := make([]Member, 0, len(team.Members))
	for _, member := range team.Members {
		if !member.Independent {
			ordered = append(ordered, member)
		}
	}
	for _, member := range team.Members {
		if member.Independent {
			ordered = append(ordered, member)
		}
	}
	return ordered
}

// tasksForRoles maps each role to the tasks it covers.
func tasksForRoles(team Team, tasks []Task) map[Role][]string {
	byRole := map[Role][]string{}
	for _, member := range team.Members {
		for _, task := range tasks {
			switch member.Role {
			case RoleQA, RoleAppSec:
				// Checking roles cover everything that changes something.
				if task.Mutating {
					byRole[member.Role] = append(byRole[member.Role], task.ID)
				}
			case RoleDeveloper:
				byRole[member.Role] = append(byRole[member.Role], task.ID)
			}
		}
		sort.Strings(byRole[member.Role])
	}
	return byRole
}
