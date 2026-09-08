package plan

import (
	"sort"
	"strconv"
	"strings"
)

// This file compares independent plan proposals and picks the best one.
//
// ULTRA's value is that several planners think about the work separately
// before anything is chosen, so a blind spot in one proposal is likely to be
// visible in another. That only holds if the proposals are genuinely
// independent, so nothing here shows one planner another's work.
//
// The comparison is deterministic and computed by MARSHAL from the proposals'
// contents. A planner's own opinion of its plan is not evidence: a model
// asked to rate its work will rate it well, and a proposal that scored itself
// highest would win by asserting rather than by being better. Any self-score
// present in a proposal is deliberately ignored.

// ProposalSource says where a proposal came from.
type ProposalSource struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	// SelfScore is whatever the planner claimed about its own plan. It is
	// recorded so a reader can see what was claimed, and is never read by the
	// comparison. Keeping it visible but inert is more honest than discarding
	// it silently.
	SelfScore string `json:"self_score,omitempty"`
}

// Proposal is one independent plan candidate.
type Proposal struct {
	ID     string         `json:"id"`
	Source ProposalSource `json:"source"`
	Tasks  []Task         `json:"tasks"`
}

// Comparison is the deterministic scoring of one proposal.
//
// Every field is derived from the proposal's own contents against the Goal, so
// two runs over the same inputs produce the same numbers and a user can be
// shown why one plan beat another.
type Comparison struct {
	ProposalID string `json:"proposal_id"`
	// GoalCoverage is how many of the Goal's criteria some task serves.
	GoalCoverage int `json:"goal_coverage"`
	// ConstraintViolations counts tasks that breach a hard constraint. Any
	// violation disqualifies outright rather than costing points.
	ConstraintViolations int `json:"constraint_violations"`
	// UnresolvedUnknowns counts things the proposal could not establish.
	UnresolvedUnknowns int `json:"unresolved_unknowns"`
	// HardApprovals counts gates a person must answer. Fewer is better only
	// as a tie-breaker: a plan avoiding a gate by not doing the work is not
	// better, which is why coverage is compared first.
	HardApprovals int `json:"hard_approvals"`
	// IrreversibleTasks counts steps that cannot be undone.
	IrreversibleTasks int `json:"irreversible_tasks"`
	// VerificationCoverage is how many criteria have a check.
	VerificationCoverage int `json:"verification_coverage"`
	// UngovernedAssignments counts work routed somewhere MARSHAL cannot
	// govern. Like a constraint violation, any is disqualifying.
	UngovernedAssignments int `json:"ungoverned_assignments"`
	// ContextExposure is how many distinct paths the plan touches — a proxy
	// for how much of the project is reachable while the work runs.
	ContextExposure int `json:"context_exposure"`
	// CollisionRisk counts pairs of tasks that would contend for the same
	// files.
	CollisionRisk int `json:"collision_risk"`
	// BudgetFeasible reports whether the work fits the allowed budget.
	BudgetFeasible bool `json:"budget_feasible"`

	// Disqualified marks a proposal that cannot be selected at all, with why.
	Disqualified bool     `json:"disqualified"`
	Reasons      []string `json:"reasons,omitempty"`
}

// Selection is the outcome of comparing proposals.
type Selection struct {
	// Best is the chosen proposal, empty if none qualified.
	Best string `json:"best,omitempty"`
	// Comparisons are every proposal's scoring, so the choice is inspectable.
	Comparisons []Comparison `json:"comparisons"`
	// Reason states why the winner won, in the user's terms.
	Reason string `json:"reason,omitempty"`
	// MergedStrengths names improvements taken from proposals that lost.
	// A losing plan can still contain the one task everyone else forgot.
	MergedStrengths []string `json:"merged_strengths,omitempty"`
}

// Chosen reports whether any proposal qualified.
func (s Selection) Chosen() bool { return s.Best != "" }

// UltraRequest is the input to proposal comparison.
type UltraRequest struct {
	Proposals []Proposal
	// Criteria are the Goal's acceptance criteria.
	Criteria []string
	// HardConstraints are the constraints no proposal may breach.
	HardConstraints []string
	// DoNotDo are explicit prohibitions.
	DoNotDo []string
	// MaxTasks is the budget ceiling, zero meaning unbounded.
	MaxTasks int
	// Ungovernable names providers MARSHAL cannot govern, so a proposal
	// routing work to one can be disqualified.
	Ungovernable map[string]bool
}

// CompareProposals scores every proposal deterministically.
func CompareProposals(request UltraRequest) []Comparison {
	comparisons := make([]Comparison, 0, len(request.Proposals))
	for _, proposal := range request.Proposals {
		comparisons = append(comparisons, compareOne(proposal, request))
	}
	sort.SliceStable(comparisons, func(a, b int) bool {
		return comparisons[a].ProposalID < comparisons[b].ProposalID
	})
	return comparisons
}

func compareOne(proposal Proposal, request UltraRequest) Comparison {
	comparison := Comparison{ProposalID: proposal.ID}

	covered := map[string]bool{}
	// verifiable holds criteria some non-mutating task serves. A read-only
	// task covering a criterion is a check on it; the task that makes the
	// change is not a check on its own work.
	verifiable := map[string]bool{}
	paths := map[string]bool{}
	for _, task := range proposal.Tasks {
		for _, criterion := range task.Criteria {
			covered[criterion] = true
			if !task.Mutating {
				verifiable[criterion] = true
			}
		}
		for _, path := range task.Paths {
			paths[path] = true
		}
		if task.Mutating {
			comparison.IrreversibleTasks += irreversibleWeight(task)
		}
		if task.RequiresApproval {
			comparison.HardApprovals++
		}

		// A task breaching a hard constraint disqualifies the whole proposal.
		// This is not a penalty to be outweighed: a plan that does the
		// forbidden thing efficiently is not a better plan.
		for _, constraint := range append(append([]string{}, request.HardConstraints...), request.DoNotDo...) {
			if breaches(task, constraint) {
				comparison.ConstraintViolations++
				comparison.Reasons = append(comparison.Reasons,
					"Task "+task.ID+" would breach a constraint: "+constraint)
			}
		}

		if request.Ungovernable[strings.ToLower(strings.TrimSpace(task.Role))] {
			comparison.UngovernedAssignments++
			comparison.Reasons = append(comparison.Reasons,
				"Task "+task.ID+" is routed somewhere MARSHAL cannot govern.")
		}
	}

	// Doing the work and being able to check it are different things, so they
	// are counted separately. A proposal whose only task for a criterion is
	// the mutating one that produces it has nothing that would verify the
	// result — the change is made and nobody looks. Counting these together
	// would make verification coverage a restatement of goal coverage and the
	// dimension would decide nothing.
	for _, criterion := range request.Criteria {
		if !covered[criterion] {
			comparison.UnresolvedUnknowns++
			continue
		}
		comparison.GoalCoverage++
		if verifiable[criterion] {
			comparison.VerificationCoverage++
		}
	}

	comparison.ContextExposure = len(paths)
	comparison.CollisionRisk = collisionPairs(proposal.Tasks)
	comparison.BudgetFeasible = request.MaxTasks == 0 || len(proposal.Tasks) <= request.MaxTasks
	if !comparison.BudgetFeasible {
		comparison.Reasons = append(comparison.Reasons, "This plan does not fit the allowed budget.")
	}

	// Disqualification is separate from ranking, so a disqualified plan can
	// never win by scoring well elsewhere.
	if comparison.ConstraintViolations > 0 || comparison.UngovernedAssignments > 0 || !comparison.BudgetFeasible {
		comparison.Disqualified = true
	}
	sort.Strings(comparison.Reasons)
	return comparison
}

// SelectBest picks the best qualifying proposal.
//
// The order of comparison encodes what MARSHAL values. Goal coverage comes
// first because a plan that does not do the job is not a candidate however
// tidy it is; verification next, because work nobody can check is not finished
// work; then the things that reduce exposure and risk. Nothing here consults a
// planner's opinion of itself.
func SelectBest(request UltraRequest) Selection {
	comparisons := CompareProposals(request)
	selection := Selection{Comparisons: comparisons}

	var qualified []Comparison
	for _, comparison := range comparisons {
		if !comparison.Disqualified {
			qualified = append(qualified, comparison)
		}
	}
	if len(qualified) == 0 {
		selection.Reason = "No proposal was usable: every one breached a constraint, exceeded the budget, or routed work somewhere MARSHAL cannot govern."
		return selection
	}

	sort.SliceStable(qualified, func(a, b int) bool {
		first, second := qualified[a], qualified[b]
		if first.GoalCoverage != second.GoalCoverage {
			return first.GoalCoverage > second.GoalCoverage
		}
		if first.VerificationCoverage != second.VerificationCoverage {
			return first.VerificationCoverage > second.VerificationCoverage
		}
		// UnresolvedUnknowns is deliberately not a ranking key. Over a fixed
		// set of criteria it is exactly len(criteria) - GoalCoverage, so
		// ranking on it would decide nothing that coverage has not already
		// decided, while making the ordering look like it weighs more
		// evidence than it does. It is still reported, because "two criteria
		// are still uncovered" is worth telling a user.
		if first.IrreversibleTasks != second.IrreversibleTasks {
			return first.IrreversibleTasks < second.IrreversibleTasks
		}
		if first.CollisionRisk != second.CollisionRisk {
			return first.CollisionRisk < second.CollisionRisk
		}
		if first.ContextExposure != second.ContextExposure {
			return first.ContextExposure < second.ContextExposure
		}
		if first.HardApprovals != second.HardApprovals {
			return first.HardApprovals < second.HardApprovals
		}
		// A stable final tie-break on identity, so equal proposals do not
		// swap places between runs.
		return first.ProposalID < second.ProposalID
	})

	best := qualified[0]
	selection.Best = best.ProposalID
	selection.Reason = describeWin(best, qualified)
	selection.MergedStrengths = mergeableStrengths(best, request)
	return selection
}

// describeWin states why the winner won, using the figures rather than an
// adjective.
func describeWin(best Comparison, qualified []Comparison) string {
	reason := "This plan covers " + plural(best.GoalCoverage, "acceptance criterion", "acceptance criteria") + "."
	if len(qualified) > 1 {
		reason += " It was compared against " +
			plural(len(qualified)-1, "other usable proposal", "other usable proposals") +
			" on coverage, verification, reversibility and exposure."
	}
	if best.UnresolvedUnknowns > 0 {
		reason += " " + plural(best.UnresolvedUnknowns, "criterion is", "criteria are") +
			" still uncovered."
	}
	return reason
}

// mergeableStrengths names things a losing proposal covered that the winner
// did not.
//
// Merging is limited to naming the gap rather than splicing tasks together.
// A task lifted out of the plan it was designed for loses the context that
// made it correct, and silently combining two planners' work would produce a
// plan neither of them checked.
func mergeableStrengths(best Comparison, request UltraRequest) []string {
	if best.UnresolvedUnknowns == 0 {
		return nil
	}
	var winner Proposal
	for _, proposal := range request.Proposals {
		if proposal.ID == best.ProposalID {
			winner = proposal
		}
	}
	covered := map[string]bool{}
	for _, task := range winner.Tasks {
		for _, criterion := range task.Criteria {
			covered[criterion] = true
		}
	}

	var strengths []string
	for _, proposal := range request.Proposals {
		if proposal.ID == best.ProposalID {
			continue
		}
		for _, task := range proposal.Tasks {
			for _, criterion := range task.Criteria {
				if !covered[criterion] {
					strengths = append(strengths,
						"Proposal "+proposal.ID+" covers \""+criterion+"\", which the selected plan does not.")
					covered[criterion] = true
				}
			}
		}
	}
	sort.Strings(strengths)
	return strengths
}

// breaches reports whether a task appears to violate a constraint.
//
// The check is textual and deliberately conservative: it looks for the
// constraint's distinctive words in what the task says it will touch. It can
// produce a false positive, which costs a proposal that could have been used;
// a false negative would let a forbidden change through, which is worse.
func breaches(task Task, constraint string) bool {
	subject := strings.ToLower(constraint)
	subject = strings.TrimPrefix(subject, "do not ")
	subject = strings.TrimPrefix(subject, "don't ")
	subject = strings.TrimPrefix(subject, "never ")
	words := strings.Fields(subject)

	// Only nouns long enough to be distinctive are used. Matching on short
	// common words would flag almost everything.
	var distinctive []string
	for _, word := range words {
		word = strings.Trim(word, ".,;:\"'")
		if len(word) > 4 && !commonVerb(word) {
			distinctive = append(distinctive, word)
		}
	}
	if len(distinctive) == 0 {
		return false
	}

	haystack := strings.ToLower(task.Title + " " + strings.Join(task.Paths, " "))
	matched := 0
	for _, word := range distinctive {
		if strings.Contains(haystack, word) {
			matched++
		}
	}
	// Every distinctive word must appear, so "do not modify the database
	// schema" does not fire on a task that merely mentions a database.
	return matched == len(distinctive)
}

func commonVerb(word string) bool {
	switch word {
	case "modify", "change", "touch", "alter", "update", "these", "those", "which", "their":
		return true
	}
	return false
}

// irreversibleWeight scores how hard a task is to undo.
func irreversibleWeight(task Task) int {
	lowered := strings.ToLower(task.Title)
	for _, word := range []string{"delete", "drop", "destroy", "purge", "wipe", "truncate", "migrat"} {
		if strings.Contains(lowered, word) {
			return 1
		}
	}
	return 0
}

// anyPathOverlaps reports whether two task path sets contend.
//
// It reuses the DAG's segment-aware comparison, so "internal/api" and
// "internal/apiv2" are correctly treated as separate here too. Writing a
// second, simpler comparison would let the two disagree.
func anyPathOverlaps(first, second []string) bool {
	for _, a := range first {
		for _, b := range second {
			if pathsOverlap(a, b) {
				return true
			}
		}
	}
	return false
}

// collisionPairs counts pairs of mutating tasks contending for the same paths.
func collisionPairs(tasks []Task) int {
	count := 0
	for i := 0; i < len(tasks); i++ {
		for j := i + 1; j < len(tasks); j++ {
			if !tasks[i].Mutating || !tasks[j].Mutating {
				continue
			}
			if anyPathOverlaps(tasks[i].Paths, tasks[j].Paths) {
				count++
			}
		}
	}
	return count
}

func plural(count int, singular, pluralForm string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + pluralForm
}
