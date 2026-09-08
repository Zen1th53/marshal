package plan

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// State is where a plan stands.
type State string

const (
	// StateDraft means the plan exists and is not finished.
	StateDraft State = "DRAFT"
	// StateNeedsInput means something must be answered before it can be ready.
	StateNeedsInput State = "NEEDS_INPUT"
	// StateReady means the plan is complete and internally consistent. It is
	// not yet permission to execute: that needs approval.
	StateReady State = "READY"
	// StateApproved means a person accepted the plan. Only this state can
	// reach execution.
	StateApproved State = "APPROVED"
	// StateStale means the Goal moved after the plan was built.
	StateStale State = "STALE"
	// StateBlocked means something prevents planning or execution.
	StateBlocked State = "BLOCKED"
	// StateCancelled means the plan was abandoned.
	StateCancelled State = "CANCELLED"
)

// Executable reports whether this state permits handing work to Process 05.
//
// Only APPROVED qualifies. READY deliberately does not: a plan being complete
// and a person agreeing to it are different facts, and collapsing them would
// mean a plan could execute simply by being well-formed.
func (s State) Executable() bool { return s == StateApproved }

// Mode is Standard or ULTRA planning.
//
// The values match constitution.Mode's spelling rather than the TUI's display
// spelling, because these are the values that reach the database: the schema
// constrains mode to this vocabulary, and letting two spellings of one mode
// through would make stored plans inconsistent with stored decisions.
type Mode string

const (
	ModeStandard Mode = "standard"
	ModeUltra    Mode = "ultra"
)

// Route is where a task's work will run.
type Route struct {
	// Provider and Model are the primary choice.
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	// Fallbacks are tried in order if the primary is unavailable. They are
	// pre-computed at planning time so a failure mid-execution does not
	// require a fresh decision under pressure.
	Fallbacks []string `json:"fallbacks,omitempty"`
	// Governance is the evidence-derived governance state of the primary.
	Governance constitution.GovernanceState `json:"governance"`
	// Reason explains the choice.
	Reason string `json:"reason"`
}

// ApprovalRequirement is an approval the plan says will be needed.
//
// It is a requirement, never an approval. Process 04 plans that a human will
// be asked; it cannot record that they agreed, because nobody has been asked
// yet. Conflating the two would let planning approve its own work.
//
// There is deliberately no field here recording that an approval was granted.
// That absence is the invariant: a provider claiming "pre-approved, skip
// approval" arrives as text with nothing to set, so the guarantee rests on
// what this type cannot express rather than on a parser remembering to ignore
// a phrase.
type ApprovalRequirement struct {
	// Task is the task that needs it.
	Task string `json:"task"`
	// Kind classifies why, so the user is asked a specific question rather
	// than a general one.
	Kind ApprovalKind `json:"kind,omitempty"`
	// Reason is why, in the user's terms.
	Reason string `json:"reason"`
	// Hard marks an approval no delegation can cover.
	Hard bool `json:"hard"`
}

// Checkpoint is a point after which state can be restored.
type Checkpoint struct {
	// AfterTask is the task this checkpoint follows.
	AfterTask string `json:"after_task"`
	// Reason explains why a restore point belongs here.
	Reason string `json:"reason"`
}

// Budget is what the plan is allowed to consume.
type Budget struct {
	// MaxTasks bounds how much work runs before returning to the user.
	MaxTasks int `json:"max_tasks"`
	// RequiresCheckIn reports that execution pauses for the user partway.
	RequiresCheckIn bool `json:"requires_check_in"`
	// Reason explains the allocation.
	Reason string `json:"reason"`
}

// ExecutionPlan is the canonical output of Process 04.
type ExecutionPlan struct {
	ID        string       `json:"id"`
	ProjectID projectid.ID `json:"project_id"`
	// Goal binds the plan to the exact Goal it was built from.
	Goal GoalBinding `json:"goal"`
	// Version is the CAS revision. A write must name the version it read.
	Version int64 `json:"version"`
	State   State `json:"state"`
	Mode    Mode  `json:"mode"`
	// ConstitutionVersion records the rules the plan was formed under.
	ConstitutionVersion constitution.Version `json:"constitution_version"`

	// Assessment is inherited from Process 03 rather than recomputed. Two
	// disagreeing risk figures would be worse than one.
	Assessment goalintake.Assessment `json:"assessment"`

	// HardConstraints and DoNotDo are carried on the plan rather than looked
	// up from the Goal when needed. They are restated into every context
	// package and every handoff, and a constraint that travels by reference is
	// one a failed lookup can silently drop.
	HardConstraints []string `json:"hard_constraints,omitempty"`
	DoNotDo         []string `json:"do_not_do,omitempty"`

	Tasks []Task `json:"tasks"`
	Graph Graph  `json:"graph"`
	Team  Team   `json:"team"`
	// Assignments are the governed harness, model and native configuration per
	// role. Routes remain the per-task provider choice; assignments are the
	// fuller picture the harness layer produces.
	Assignments AssignmentPlan   `json:"assignments"`
	Routes      map[string]Route `json:"routes,omitempty"`
	// Policy is the per-task capability and scope envelope, plus the approval
	// gates the plan predicts.
	Policy       PolicyPlan            `json:"policy"`
	Approvals    []ApprovalRequirement `json:"approvals,omitempty"`
	Checkpoints  []Checkpoint          `json:"checkpoints,omitempty"`
	Verification VerificationPlan      `json:"verification"`
	Budget       Budget                `json:"budget"`

	// Unknowns are things planning could not establish. Recording them is what
	// stops a plan looking more certain than it is.
	Unknowns []string `json:"unknowns,omitempty"`
	// BlockedBy are conditions preventing readiness.
	BlockedBy []string `json:"blocked_by,omitempty"`
	// RevisionReason records why this version exists.
	RevisionReason string `json:"revision_reason,omitempty"`
	// Supersedes is the plan version this one replaces.
	Supersedes int64 `json:"supersedes,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BuildRequest carries everything needed to build a plan.
type BuildRequest struct {
	Goal       model.GoalContract
	ProjectID  projectid.ID
	Assessment goalintake.Assessment
	Tasks      []Task
	Mode       Mode
	Version    constitution.Version
	// Candidates are the providers available for routing.
	Candidates []goalintake.Candidate
	// Scope is the project's working scope, used as a task's allowed scope
	// when the task does not name paths of its own.
	Scope []string
	Now   time.Time
}

// Build produces a plan from a validated Goal.
//
// It fails closed: a Goal that cannot be planned from produces a BLOCKED plan
// carrying the reasons rather than an error the caller might ignore. A plan
// object that says why it is blocked is more useful than no object at all,
// because it can be shown to a user.
func Build(request BuildRequest) (ExecutionPlan, error) {
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	mode := request.Mode
	if mode == "" {
		mode = ModeStandard
	}

	executionPlan := ExecutionPlan{
		ID:                  planID(request.Goal.ID, request.Goal.Revision),
		ProjectID:           request.ProjectID,
		Goal:                BindGoal(request.Goal),
		Version:             1,
		Mode:                mode,
		ConstitutionVersion: request.Version,
		Assessment:          request.Assessment,
		Tasks:               request.Tasks,
		DoNotDo:             append([]string(nil), request.Goal.DoNotDo...),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	// Hard constraints are copied onto the plan at build time so they can be
	// restated into every context package and handoff without a lookup that
	// could fail.
	for _, constraint := range request.Goal.Constraints {
		if constraint.IsHard {
			executionPlan.HardConstraints = append(executionPlan.HardConstraints, constraint.Text)
		}
	}
	sort.Strings(executionPlan.HardConstraints)

	if validation := ValidateGoal(request.Goal, request.ProjectID); validation.Blocked() {
		executionPlan.State = StateBlocked
		executionPlan.BlockedBy = validation.Detail
		return executionPlan, nil
	}

	graph, err := BuildGraph(request.Tasks)
	if err != nil {
		executionPlan.State = StateBlocked
		executionPlan.BlockedBy = []string{err.Error()}
		return executionPlan, nil
	}
	executionPlan.Graph = graph

	executionPlan.Team = AssembleTeam(request.Assessment, request.Tasks, request.Goal.SuccessCriteria)
	executionPlan.Verification = PlanVerification(
		request.Goal.SuccessCriteria, request.Tasks, request.Assessment, executionPlan.Team)
	// Policy and approvals are derived together: the gates a task will meet
	// follow from the same facts as the envelope it runs under.
	executionPlan.Policy = PlanPolicy(request.Tasks, request.Assessment, request.Scope)
	executionPlan.Approvals = executionPlan.Policy.Approvals
	executionPlan.Checkpoints = planCheckpoints(request.Tasks, graph, request.Assessment)
	executionPlan.Budget = allocateBudget(request.Tasks, request.Assessment)
	executionPlan.Routes, executionPlan.Unknowns = planRoutes(request.Tasks, request.Candidates)

	executionPlan.State, executionPlan.BlockedBy = resolveState(executionPlan)
	return executionPlan, nil
}

// resolveState decides where a freshly built plan stands.
//
// The conditions are all things that would make execution unsafe or
// unverifiable, and each produces a reason a user can act on.
func resolveState(executionPlan ExecutionPlan) (State, []string) {
	var blockers []string

	// A criterion with no verification path means the work's success could not
	// be established. Shipping that is how "done" stops meaning anything.
	if !executionPlan.Verification.Complete() {
		blockers = append(blockers,
			"These success criteria have nothing in the plan that would check them: "+
				strings.Join(executionPlan.Verification.Uncovered, ", ")+".")
	}
	// A task with no route cannot run.
	for _, task := range executionPlan.Tasks {
		if _, routed := executionPlan.Routes[task.ID]; !routed {
			blockers = append(blockers, "No provider is available for "+task.ID+".")
		}
	}
	if len(executionPlan.Tasks) == 0 {
		blockers = append(blockers, "The plan has no work in it.")
	}

	if len(blockers) > 0 {
		sort.Strings(blockers)
		return StateBlocked, blockers
	}
	return StateReady, nil
}

// planCheckpoints places restore points.
//
// One goes before the first mutating stage, because that is the boundary
// between "nothing has changed" and "something has", and it is the point a
// user most often wants to return to. Others follow tasks that asked for one.
func planCheckpoints(tasks []Task, graph Graph, assessment goalintake.Assessment) []Checkpoint {
	byID := make(map[string]Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}

	var checkpoints []Checkpoint
	seen := map[string]bool{}
	addCheckpoint := func(after, reason string) {
		if after == "" || seen[after] {
			return
		}
		seen[after] = true
		checkpoints = append(checkpoints, Checkpoint{AfterTask: after, Reason: reason})
	}

	// A restore point before the first change matters most when the work is
	// hard to undo.
	if assessment.Reversibility.AtLeast(goalintake.LevelMed) {
		for _, id := range graph.Order {
			if byID[id].Mutating {
				addCheckpoint(id, "This is where the project starts changing, and these changes are hard to undo.")
				break
			}
		}
	}
	for _, id := range graph.Order {
		if byID[id].Checkpoint {
			addCheckpoint(id, "A restore point was requested here.")
		}
	}
	return checkpoints
}

// allocateBudget bounds how much runs before the user is consulted.
//
// The bound is on work rather than on time or money, because those are the
// figures MARSHAL cannot measure honestly. A task count is something it
// actually knows.
func allocateBudget(tasks []Task, assessment goalintake.Assessment) Budget {
	budget := Budget{MaxTasks: len(tasks)}
	switch {
	case assessment.RequiresConfirmation():
		budget.RequiresCheckIn = true
		budget.Reason = "The plan pauses for you partway, because parts of this need watching."
	case len(tasks) > 8:
		budget.RequiresCheckIn = true
		budget.Reason = "The plan pauses partway so you can see how it is going before it finishes."
	default:
		budget.Reason = "The plan runs to completion without pausing."
	}
	return budget
}

// planRoutes assigns a provider to each task and pre-computes fallbacks.
//
// Routing reuses the Process 03 selector, so governance outranks capacity here
// exactly as it does at intake. A task with no usable provider is left
// unrouted rather than assigned to something ungovernable, and the reason
// becomes an unknown the plan carries.
func planRoutes(tasks []Task, candidates []goalintake.Candidate) (map[string]Route, []string) {
	if len(tasks) == 0 {
		return nil, nil
	}
	selection := goalintake.Select(candidates)
	if !selection.Usable() {
		// One unknown, not one per task: repeating the same cause for every
		// task would bury it.
		return nil, []string{selection.Reason}
	}

	route := Route{
		Provider:   selection.Provider,
		Model:      selection.Model,
		Fallbacks:  selection.Fallbacks,
		Governance: selection.Governance,
		Reason:     selection.Reason,
	}
	routes := make(map[string]Route, len(tasks))
	for _, task := range tasks {
		routes[task.ID] = route
	}

	var unknowns []string
	if selection.Degraded {
		unknowns = append(unknowns,
			"MARSHAL cannot fully confirm control of "+selection.Provider+", so it is operating in a reduced mode.")
	}
	if len(selection.Fallbacks) == 0 {
		unknowns = append(unknowns,
			"There is no alternative provider if "+selection.Provider+" becomes unavailable.")
	}
	return routes, unknowns
}

func planID(goalID string, revision int64) string {
	return fmt.Sprintf("PLAN-%s-r%d", strings.TrimPrefix(goalID, "GOAL-"), revision)
}

// Approve records a person accepting the plan.
//
// A plan that is not ready cannot be approved: approving a blocked plan would
// approve whatever the blockers were hiding.
func (p ExecutionPlan) Approve(now time.Time) (ExecutionPlan, error) {
	if p.State != StateReady {
		return p, fmt.Errorf("%w: a plan in state %s cannot be approved", ErrPlanInvalid, p.State)
	}
	approved := p
	approved.State = StateApproved
	approved.UpdatedAt = timeOrNow(now)
	return approved, nil
}

// Cancel abandons a plan.
func (p ExecutionPlan) Cancel(now time.Time) ExecutionPlan {
	cancelled := p
	cancelled.State = StateCancelled
	cancelled.UpdatedAt = timeOrNow(now)
	return cancelled
}

// Restale marks a plan stale against a changed Goal.
//
// A stale plan is kept rather than deleted, so a user can see what it was and
// why it no longer applies instead of finding the plan simply gone.
func (p ExecutionPlan) Restale(goal model.GoalContract, now time.Time) ExecutionPlan {
	if p.Goal.Matches(goal) {
		return p
	}
	stale := p
	stale.State = StateStale
	stale.BlockedBy = p.Goal.StaleAgainst(goal)
	stale.UpdatedAt = timeOrNow(now)
	return stale
}

// Revise produces the next version of a plan under CAS.
//
// The expected version must match, so two callers revising concurrently cannot
// silently overwrite one another: the second is told its view was out of date
// rather than winning by arriving later.
func (p ExecutionPlan) Revise(expectedVersion int64, reason string, now time.Time) (ExecutionPlan, error) {
	if expectedVersion != p.Version {
		return p, fmt.Errorf("%w: the plan has changed since you read it (version %d, you had %d)",
			ErrPlanInvalid, p.Version, expectedVersion)
	}
	if strings.TrimSpace(reason) == "" {
		return p, fmt.Errorf("%w: a revision must record why it was made", ErrPlanInvalid)
	}
	revised := p
	revised.Version = p.Version + 1
	revised.Supersedes = p.Version
	revised.RevisionReason = reason
	// A revised plan returns to draft: the user agreed to the previous
	// version, not this one.
	revised.State = StateDraft
	revised.UpdatedAt = timeOrNow(now)
	return revised, nil
}

func timeOrNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now
}
