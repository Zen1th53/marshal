// Package plan turns a confirmed Goal into an execution-ready plan.
//
// Process 04 sits between understanding what a user wants and doing it. Its
// output is a plan: what work there is, in what order, who does it, on which
// provider, under which approvals, with what verification, and what to do when
// something fails. Nothing here executes any of it — creating a plan and
// carrying it out are separate acts, and keeping them separate is what lets a
// user read a plan before it happens.
//
// The package inherits rather than recomputes. Risk comes from the Process 03
// assessment, project identity from Process 02, governance from Process 00. A
// second opinion computed here could drift from the first, and two disagreeing
// risk figures are worse than one.
package plan

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// ErrPlanInvalid reports a plan that cannot be built or used.
var ErrPlanInvalid = errors.New("plan is invalid")

// BlockReason names why planning cannot proceed. The reasons are distinct
// because they call for different responses: a stale Goal needs re-confirming,
// a mismatched project needs the user to open the right one, and a failed
// integrity check needs investigating rather than retrying.
type BlockReason string

const (
	BlockGoalNotFound        BlockReason = "GOAL_NOT_FOUND"
	BlockGoalStale           BlockReason = "GOAL_STALE"
	BlockGoalCancelled       BlockReason = "GOAL_CANCELLED"
	BlockProjectMismatch     BlockReason = "PROJECT_MISMATCH"
	BlockConstraintIntegrity BlockReason = "CONSTRAINT_INTEGRITY_FAILED"
	BlockConfirmationNeeded  BlockReason = "CONFIRMATION_REQUIRED"
)

// Explain renders a block reason for a user.
func (r BlockReason) Explain() string {
	switch r {
	case BlockGoalNotFound:
		return "There is no goal to plan from."
	case BlockGoalStale:
		return "The goal changed after this plan was made, so the plan no longer matches it."
	case BlockGoalCancelled:
		return "This goal was cancelled."
	case BlockProjectMismatch:
		return "This goal belongs to a different project."
	case BlockConstraintIntegrity:
		return "The goal's constraints could not be verified."
	case BlockConfirmationNeeded:
		return "This goal has not been confirmed yet."
	default:
		return "Planning cannot start."
	}
}

// Validation is the outcome of checking a Goal before planning.
type Validation struct {
	// OK reports whether planning may proceed.
	OK bool `json:"ok"`
	// Reasons are the blocking conditions, in stable order.
	Reasons []BlockReason `json:"reasons,omitempty"`
	// Detail is user-safe.
	Detail []string `json:"detail,omitempty"`
}

// Blocked reports whether any condition prevents planning.
func (v Validation) Blocked() bool { return !v.OK }

// ValidateGoal checks that a Goal can be planned from.
//
// It is deliberately strict about identity and confirmation. Planning against
// the wrong project, an unconfirmed goal or one whose constraints cannot be
// accounted for produces work the user did not ask for, and the cost of
// finding that out later is much higher than the cost of refusing now.
func ValidateGoal(goal model.GoalContract, project projectid.ID) Validation {
	validation := Validation{OK: true}
	block := func(reason BlockReason, detail string) {
		validation.OK = false
		validation.Reasons = append(validation.Reasons, reason)
		validation.Detail = append(validation.Detail, detail)
	}

	if strings.TrimSpace(goal.ID) == "" || goal.Revision < 1 {
		block(BlockGoalNotFound, BlockGoalNotFound.Explain())
		// Nothing further can be said about a goal that is not there.
		return validation
	}
	if goal.Confirmation == model.ConfirmationCancelled {
		block(BlockGoalCancelled, BlockGoalCancelled.Explain())
	}
	if !goal.Confirmation.Settled() && goal.Confirmation != model.ConfirmationCancelled {
		// Pending and needs-input are both "the user has not agreed", which is
		// the condition that matters; the difference is shown by the Goal.
		block(BlockConfirmationNeeded, BlockConfirmationNeeded.Explain())
	}
	if !project.Valid() {
		block(BlockProjectMismatch, "The current project has no confirmed identity.")
	} else if goal.ProjectID != "" && goal.ProjectID != string(project) {
		block(BlockProjectMismatch, BlockProjectMismatch.Explain())
	} else if goal.ProjectID == "" {
		// A goal predating project binding cannot be shown to belong here.
		// Planning from it would silently attribute work to a project it may
		// not concern.
		block(BlockProjectMismatch, "This goal does not record which project it belongs to.")
	}
	// The original request is what every later check measures against. Without
	// it a plan cannot be shown to serve what was asked.
	if strings.TrimSpace(goal.OriginalRequest) == "" {
		block(BlockConstraintIntegrity, "The goal does not record the original request.")
	}
	// A hard constraint with no text cannot be honoured or checked, so it is
	// treated as an integrity failure rather than ignored.
	for _, constraint := range goal.Constraints {
		if constraint.IsHard && strings.TrimSpace(constraint.Text) == "" {
			block(BlockConstraintIntegrity, "A constraint on this goal has no content.")
			break
		}
	}
	return validation
}

// GoalBinding is the exact Goal a plan was built from.
//
// A plan carries this rather than a pointer, so that a later Goal revision
// makes the mismatch visible instead of silently changing what the plan was
// for. Comparing the two is how staleness is detected.
type GoalBinding struct {
	GoalID    string `json:"goal_id"`
	Revision  int64  `json:"revision"`
	ProjectID string `json:"project_id"`
	// RequestDigest binds to the exact request text. A goal re-formed from a
	// changed request produces a different digest even at the same revision.
	RequestDigest string `json:"request_digest"`
	// ConstraintDigest covers the hard constraints, so a constraint added or
	// removed stales the plan even when nothing else changed.
	ConstraintDigest string `json:"constraint_digest"`
}

// BindGoal captures the identity of the Goal a plan is built from.
func BindGoal(goal model.GoalContract) GoalBinding {
	return GoalBinding{
		GoalID:           goal.ID,
		Revision:         goal.Revision,
		ProjectID:        goal.ProjectID,
		RequestDigest:    goal.RequestDigest,
		ConstraintDigest: hardConstraintDigest(goal),
	}
}

// Matches reports whether a Goal is still the one a plan was built from.
func (b GoalBinding) Matches(goal model.GoalContract) bool {
	return b.GoalID == goal.ID &&
		b.Revision == goal.Revision &&
		b.ProjectID == goal.ProjectID &&
		b.RequestDigest == goal.RequestDigest &&
		b.ConstraintDigest == hardConstraintDigest(goal)
}

// StaleAgainst explains how a Goal has diverged from a plan's binding, so a
// user is told what changed rather than only that something did.
func (b GoalBinding) StaleAgainst(goal model.GoalContract) []string {
	var reasons []string
	if b.GoalID != goal.ID {
		reasons = append(reasons, "This plan was made for a different goal.")
	}
	if b.Revision != goal.Revision {
		reasons = append(reasons, "The goal has been revised since this plan was made.")
	}
	if b.ProjectID != goal.ProjectID {
		reasons = append(reasons, "The goal now belongs to a different project.")
	}
	if b.RequestDigest != goal.RequestDigest {
		reasons = append(reasons, "The original request has changed.")
	}
	if b.ConstraintDigest != hardConstraintDigest(goal) {
		reasons = append(reasons, "The goal's constraints have changed.")
	}
	return reasons
}

// hardConstraintDigest fingerprints the hard constraints.
//
// Only hard constraints are covered. A soft preference changing is not a
// reason to invalidate a plan, and treating it as one would make plans stale
// so often that the signal would stop being read.
func hardConstraintDigest(goal model.GoalContract) string {
	var parts []string
	for _, constraint := range goal.Constraints {
		if constraint.IsHard {
			parts = append(parts, constraint.ID+"="+constraint.Text)
		}
	}
	parts = append(parts, goal.DoNotDo...)
	sortStrings(parts)
	return digest(strings.Join(parts, "\x1f"))
}

// RequireCurrentGoal checks a plan's binding against the live Goal.
//
// This is the check Process 05 depends on: a plan may only be executed against
// the Goal it was built from. Without it, revising a goal after approving a
// plan would run the old plan under the new goal's authority.
func RequireCurrentGoal(binding GoalBinding, goal model.GoalContract) error {
	if binding.Matches(goal) {
		return nil
	}
	reasons := binding.StaleAgainst(goal)
	return fmt.Errorf("%w: %s", ErrPlanInvalid, strings.Join(reasons, " "))
}
