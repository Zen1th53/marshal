package plan

import (
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// This file is the boundary between planning and doing.
//
// Everything upstream produces a plan; nothing upstream may run it. The gate
// here is what makes that separation real rather than a matter of discipline:
// Process 05 receives a handoff or it receives a refusal, and there is no
// third path by which work starts from a plan that did not pass.
//
// The checks are deliberately re-run at the boundary even though the plan was
// checked when it was built. Time passes between planning and execution — a
// goal can be revised, a constraint added, an approval given and withdrawn —
// and a check performed earlier proves something about earlier.

// Handoff is what Process 05 receives.
//
// It carries canonical state and nothing else. There is no provider
// conversation, no model output and no transcript, because Process 05 must be
// able to start on a provider that has never seen this work.
type Handoff struct {
	ProjectID projectid.ID `json:"project_id"`
	// Goal identifies exactly which Goal revision authorizes this work.
	Goal GoalBinding `json:"goal"`
	// OriginalRequest travels with the handoff so execution can be checked
	// against what was asked rather than against the plan's paraphrase of it.
	OriginalRequest string `json:"original_request"`
	// HardConstraints are restated rather than referenced, so they cannot be
	// lost by a lookup failing.
	HardConstraints []string `json:"hard_constraints,omitempty"`

	PlanID              string               `json:"plan_id"`
	PlanVersion         int64                `json:"plan_version"`
	ConstitutionVersion constitution.Version `json:"constitution_version"`

	Tasks        []Task                `json:"tasks"`
	Graph        Graph                 `json:"graph"`
	Team         Team                  `json:"team"`
	Routes       map[string]Route      `json:"routes"`
	Approvals    []ApprovalRequirement `json:"approvals,omitempty"`
	Checkpoints  []Checkpoint          `json:"checkpoints,omitempty"`
	Verification VerificationPlan      `json:"verification"`
	Budget       Budget                `json:"budget"`
	// Unknowns travel with the handoff, so execution begins knowing what
	// planning could not establish rather than assuming it was all settled.
	Unknowns []string  `json:"unknowns,omitempty"`
	IssuedAt time.Time `json:"issued_at"`
}

// HandoffRefusal explains why work cannot start.
type HandoffRefusal struct {
	Reasons []string `json:"reasons"`
}

// Error makes a refusal usable as an error without losing its structure.
func (r *HandoffRefusal) Error() string {
	return "cannot start work: " + strings.Join(r.Reasons, " ")
}

// PrepareHandoff checks a plan against live state and produces a handoff.
//
// It re-verifies rather than trusting the plan's recorded state. The plan says
// it was ready when it was built; the gate asks whether it is ready now, and
// those are different questions once any time has passed.
func PrepareHandoff(executionPlan ExecutionPlan, goal model.GoalContract, project projectid.ID, now time.Time) (Handoff, error) {
	refusal := &HandoffRefusal{}
	refuse := func(reason string) { refusal.Reasons = append(refusal.Reasons, reason) }

	// The Goal must still be the one the plan was built from. Without this,
	// revising a goal after approving a plan would run the old plan under the
	// new goal's authority.
	if err := RequireCurrentGoal(executionPlan.Goal, goal); err != nil {
		for _, reason := range executionPlan.Goal.StaleAgainst(goal) {
			refuse(reason)
		}
	}
	// The Goal must still be confirmed. An approval withdrawn between
	// planning and execution has to take effect.
	if !goal.Confirmation.Settled() {
		refuse("The goal is no longer confirmed.")
	}
	// The project must still match.
	if !project.Valid() || executionPlan.ProjectID != project {
		refuse("This plan belongs to a different project.")
	}
	// Only an approved plan executes. A ready plan is complete, not agreed to.
	if !executionPlan.State.Executable() {
		refuse("This plan is " + string(executionPlan.State) + " and has not been approved for execution.")
	}
	// The plan must still be internally sound. These were checked at build
	// time; re-checking costs nothing and catches a plan mutated in between.
	if len(executionPlan.Tasks) == 0 {
		refuse("The plan has no work in it.")
	}
	if len(executionPlan.Graph.Order) != len(executionPlan.Tasks) {
		refuse("The plan's task order does not cover its tasks.")
	}
	if executionPlan.Graph.Digest != "" {
		if current := graphDigestFor(executionPlan.Tasks); current != executionPlan.Graph.Digest {
			refuse("The plan's tasks have changed since it was built.")
		}
	}
	if !executionPlan.Verification.Complete() {
		refuse("Some success criteria have nothing that would check them.")
	}
	for _, task := range executionPlan.Tasks {
		if _, routed := executionPlan.Routes[task.ID]; !routed {
			refuse("No provider is available for " + task.ID + ".")
		}
	}
	// A route to a provider MARSHAL cannot govern is not a route.
	for id, route := range executionPlan.Routes {
		if route.Governance == constitution.GovernanceUnavailable {
			refuse("The provider for " + id + " cannot be governed.")
		}
	}
	if len(executionPlan.BlockedBy) > 0 {
		refusal.Reasons = append(refusal.Reasons, executionPlan.BlockedBy...)
	}

	if len(refusal.Reasons) > 0 {
		sort.Strings(refusal.Reasons)
		return Handoff{}, refusal
	}

	handoff := Handoff{
		ProjectID:           executionPlan.ProjectID,
		Goal:                executionPlan.Goal,
		OriginalRequest:     goal.OriginalRequest,
		PlanID:              executionPlan.ID,
		PlanVersion:         executionPlan.Version,
		ConstitutionVersion: executionPlan.ConstitutionVersion,
		Tasks:               executionPlan.Tasks,
		Graph:               executionPlan.Graph,
		Team:                executionPlan.Team,
		Routes:              executionPlan.Routes,
		Approvals:           executionPlan.Approvals,
		Checkpoints:         executionPlan.Checkpoints,
		Verification:        executionPlan.Verification,
		Budget:              executionPlan.Budget,
		Unknowns:            executionPlan.Unknowns,
		IssuedAt:            timeOrNow(now),
	}
	for _, constraint := range goal.Constraints {
		if constraint.IsHard {
			handoff.HardConstraints = append(handoff.HardConstraints, constraint.Text)
		}
	}
	handoff.HardConstraints = append(handoff.HardConstraints, goal.DoNotDo...)
	sort.Strings(handoff.HardConstraints)
	return handoff, nil
}

// graphDigestFor recomputes a task set's digest, so the gate can tell whether
// the tasks it is about to hand over are the ones the plan described.
func graphDigestFor(tasks []Task) string {
	byID := make(map[string]Task, len(tasks))
	var ids []string
	for _, task := range tasks {
		byID[task.ID] = task
		ids = append(ids, task.ID)
	}
	sort.Strings(ids)
	return graphDigest(ids, byID)
}

// Validate checks a handoff carries everything Process 05 requires.
//
// Process 05 calls this on receipt rather than trusting the sender. A handoff
// missing a required field is refused there too, so a defect on this side
// cannot become an execution that started without its constraints.
func (h Handoff) Validate() error {
	refusal := &HandoffRefusal{}
	require := func(ok bool, reason string) {
		if !ok {
			refusal.Reasons = append(refusal.Reasons, reason)
		}
	}

	require(h.ProjectID.Valid(), "The handoff names no project.")
	require(strings.TrimSpace(h.Goal.GoalID) != "", "The handoff names no goal.")
	require(h.Goal.Revision >= 1, "The handoff names no goal revision.")
	require(strings.TrimSpace(h.OriginalRequest) != "",
		"The handoff does not carry the original request.")
	require(strings.TrimSpace(h.PlanID) != "", "The handoff names no plan.")
	require(h.PlanVersion >= 1, "The handoff names no plan version.")
	require(!h.ConstitutionVersion.IsZero(), "The handoff is not bound to a constitution version.")
	require(len(h.Tasks) > 0, "The handoff carries no work.")
	require(len(h.Graph.Order) == len(h.Tasks), "The handoff's task order does not cover its tasks.")
	require(len(h.Team.Members) > 0, "The handoff names nobody to do the work.")
	require(h.Verification.Complete(), "The handoff leaves success criteria unchecked.")
	for _, task := range h.Tasks {
		require(h.Routes[task.ID].Provider != "", "The handoff has no provider for "+task.ID+".")
	}

	if len(refusal.Reasons) > 0 {
		sort.Strings(refusal.Reasons)
		return refusal
	}
	return nil
}
