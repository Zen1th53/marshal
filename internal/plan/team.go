package plan

import (
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/goalintake"
)

// This file selects the minimum sufficient team.
//
// The default is not "every role". Instantiating an architect, a developer, a
// QA engineer and a security reviewer for a typo fix costs budget and attention
// and produces four opinions where one was needed. It also devalues the roles:
// if AppSec reviews everything, its involvement stops signalling that
// something touches security.
//
// So roles are added because something about the work calls for them, and the
// reason is recorded alongside each one.

// Role is a fixed team role. The set matches the roles the schema already
// recognises, so a planned team can be instantiated without translation.
type Role string

const (
	RoleOrchestrator Role = "orchestrator"
	RoleArchitect    Role = "architect"
	RoleDeveloper    Role = "developer"
	RoleQA           Role = "qa"
	RoleAppSec       Role = "appsec"
)

// Member is one role on the team, with why it is there.
type Member struct {
	Role Role `json:"role"`
	// Reason is user-facing and names what called for this role.
	Reason string `json:"reason"`
	// Independent marks a role that must not review its own work. A verifier
	// that produced the thing it verifies is not a verifier.
	Independent bool `json:"independent,omitempty"`
}

// Team is the selected set of roles.
type Team struct {
	Members []Member `json:"members"`
	// Excluded records roles considered and left out, with why. Showing the
	// reasoning makes a small team inspectable rather than looking like an
	// oversight.
	Excluded []Member `json:"excluded,omitempty"`
}

// Has reports whether a role is on the team.
func (t Team) Has(role Role) bool {
	for _, member := range t.Members {
		if member.Role == role {
			return true
		}
	}
	return false
}

// Roles returns the selected roles in stable order.
func (t Team) Roles() []Role {
	roles := make([]Role, 0, len(t.Members))
	for _, member := range t.Members {
		roles = append(roles, member.Role)
	}
	sort.Slice(roles, func(a, b int) bool { return roles[a] < roles[b] })
	return roles
}

// IndependentVerifier returns the role that must verify the work
// independently, when the assessment calls for one.
func (t Team) IndependentVerifier() (Role, bool) {
	for _, member := range t.Members {
		if member.Independent {
			return member.Role, true
		}
	}
	return "", false
}

// AssembleTeam picks the roles the work actually calls for.
//
// Every role beyond the developer is justified by something observable: a
// safety dimension the assessment raised, a task that mutates state, or an
// acceptance criterion that needs checking. Nothing is added "to be safe",
// because a role added for no stated reason cannot later be removed for one.
func AssembleTeam(assessment goalintake.Assessment, tasks []Task, criteria []string) Team {
	var team Team
	add := func(role Role, reason string, independent bool) {
		if team.Has(role) {
			return
		}
		team.Members = append(team.Members, Member{Role: role, Reason: reason, Independent: independent})
	}
	exclude := func(role Role, reason string) {
		if team.Has(role) {
			return
		}
		team.Excluded = append(team.Excluded, Member{Role: role, Reason: reason})
	}

	mutating := false
	for _, task := range tasks {
		if task.Mutating {
			mutating = true
			break
		}
	}

	// Someone has to do the work.
	if mutating {
		add(RoleDeveloper, "The work changes the project.", false)
	} else {
		add(RoleDeveloper, "The work inspects the project.", false)
	}

	// Architecture is called for by reach or by genuine complexity — this is
	// the one place complexity legitimately drives a decision, because a large
	// change benefits from someone holding its shape even when it is safe.
	switch {
	case assessment.BlastRadius.AtLeast(goalintake.LevelMed):
		add(RoleArchitect, "The change reaches a large part of the project.", false)
	case assessment.Complexity.AtLeast(goalintake.LevelHigh):
		add(RoleArchitect, "The work is substantial enough to need a shape agreed up front.", false)
	default:
		exclude(RoleArchitect, "The change is contained enough not to need separate design.")
	}

	// Verification is called for when there is something to verify against, or
	// when the work is hard to undo. Independence matters most exactly there:
	// a change that cannot be rolled back is one whose check must not be done
	// by whoever made it.
	switch {
	case assessment.Reversibility.AtLeast(goalintake.LevelMed):
		add(RoleQA, "This would be difficult to undo, so it is checked by someone other than whoever makes the change.", true)
	case len(criteria) > 0:
		add(RoleQA, "The goal has acceptance criteria that need checking.", true)
	case assessment.VerificationDifficulty.AtLeast(goalintake.LevelMed):
		add(RoleQA, "Confirming this worked is not straightforward.", true)
	default:
		exclude(RoleQA, "There is nothing here that needs independent checking.")
	}

	// Security review is called for by the dimensions that describe exposure.
	// Keeping the trigger narrow is what keeps AppSec's involvement meaningful.
	switch {
	case assessment.DataSensitivity.AtLeast(goalintake.LevelMed):
		add(RoleAppSec, "This involves data worth being careful with.", true)
	case assessment.Privilege.AtLeast(goalintake.LevelMed):
		add(RoleAppSec, "This needs access beyond the ordinary.", true)
	case assessment.ExternalEffects.AtLeast(goalintake.LevelHigh):
		add(RoleAppSec, "This reaches systems outside the project.", true)
	default:
		exclude(RoleAppSec, "The work does not touch credentials, sensitive data or external systems.")
	}

	// Coordination is worth a role only when there is genuinely concurrent
	// work to coordinate. On a linear plan it is overhead.
	if parallelWork(tasks) {
		add(RoleOrchestrator, "Several pieces of work run at the same time.", false)
	} else {
		exclude(RoleOrchestrator, "The work runs in sequence and does not need coordinating.")
	}

	sort.SliceStable(team.Members, func(a, b int) bool { return team.Members[a].Role < team.Members[b].Role })
	sort.SliceStable(team.Excluded, func(a, b int) bool { return team.Excluded[a].Role < team.Excluded[b].Role })
	return team
}

// parallelWork reports whether any two tasks could run at the same time.
func parallelWork(tasks []Task) bool {
	if len(tasks) < 2 {
		return false
	}
	graph, err := BuildGraph(tasks)
	if err != nil {
		// An unresolvable graph is not evidence of parallelism.
		return false
	}
	for _, stage := range graph.Stages {
		if len(stage) > 1 {
			return true
		}
	}
	return false
}

// VerificationObligation is a commitment to check something before the work
// can be called done.
//
// Process 04 records obligations; it never marks anything verified. The
// distinction is the whole reason this type exists separately from evidence:
// planning to check something and having checked it are different claims, and
// a plan that could mark its own work verified would be checking itself.
type VerificationObligation struct {
	// Criterion is the acceptance criterion or claim being checked.
	Criterion string `json:"criterion"`
	// Method is how it will be checked, in the user's terms.
	Method string `json:"method"`
	// Independent reports that the check must not be performed by whoever did
	// the work.
	Independent bool `json:"independent"`
	// Critical marks a criterion the work cannot be considered done without.
	Critical bool `json:"critical"`
	// Tasks are the tasks whose output this obligation covers.
	Tasks []string `json:"tasks,omitempty"`
}

// VerificationPlan is the full set of obligations plus what is left uncovered.
type VerificationPlan struct {
	Obligations []VerificationObligation `json:"obligations"`
	// Uncovered names criteria with no verification path. A plan cannot be
	// ready while any critical criterion is here: shipping work whose success
	// nobody can check is how "done" stops meaning anything.
	Uncovered []string `json:"uncovered,omitempty"`
}

// Complete reports whether every criterion has a verification path.
func (v VerificationPlan) Complete() bool { return len(v.Uncovered) == 0 }

// PlanVerification derives verification obligations from the Goal's criteria.
//
// A criterion with no obligation is reported rather than quietly dropped. The
// alternative — inventing a plausible-sounding check — is verification theatre:
// it would let a plan look complete while proving nothing.
func PlanVerification(criteria []string, tasks []Task, assessment goalintake.Assessment, team Team) VerificationPlan {
	verificationPlan := VerificationPlan{}
	_, independentRequired := team.IndependentVerifier()

	byCriterion := map[string][]string{}
	for _, task := range tasks {
		for _, criterion := range task.Criteria {
			byCriterion[criterion] = append(byCriterion[criterion], task.ID)
		}
	}

	for _, criterion := range criteria {
		covering := byCriterion[criterion]
		if len(covering) == 0 {
			// Nothing in the plan serves this criterion, so nothing can check
			// it. Naming it is the honest outcome.
			verificationPlan.Uncovered = append(verificationPlan.Uncovered, criterion)
			continue
		}
		sort.Strings(covering)
		verificationPlan.Obligations = append(verificationPlan.Obligations, VerificationObligation{
			Criterion:   criterion,
			Method:      methodFor(criterion),
			Independent: independentRequired,
			// A criterion is critical when the work would be hard to undo:
			// getting it wrong is expensive to correct.
			Critical: assessment.Reversibility.AtLeast(goalintake.LevelMed),
			Tasks:    covering,
		})
	}

	sort.SliceStable(verificationPlan.Obligations, func(a, b int) bool {
		return verificationPlan.Obligations[a].Criterion < verificationPlan.Obligations[b].Criterion
	})
	sort.Strings(verificationPlan.Uncovered)
	return verificationPlan
}

// methodFor suggests how a criterion will be checked.
//
// The suggestions are deliberately generic. Naming a specific command MARSHAL
// has not run would be a claim about this project's tooling that planning
// cannot support; the concrete method is settled when the check is actually
// arranged.
func methodFor(criterion string) string {
	lowered := strings.ToLower(criterion)
	switch {
	case strings.Contains(lowered, "test"):
		return "run the project's tests"
	case strings.Contains(lowered, "build") || strings.Contains(lowered, "compile"):
		return "build the project"
	case strings.Contains(lowered, "security") || strings.Contains(lowered, "vulnerab"):
		return "security review with evidence"
	case strings.Contains(lowered, "performance") || strings.Contains(lowered, "latency"):
		return "measure against the current behaviour"
	case strings.Contains(lowered, "document"):
		return "read the result and confirm it says what it should"
	default:
		return "inspect the change against the criterion"
	}
}
