package goalintake

import (
	"fmt"
	"sort"

	"github.com/Zen1th53/marshal/internal/model"
)

// This file decides who confirms a Goal.
//
// ULTRA can delegate ordinary confirmation. The question this file answers is
// which confirmations are ordinary, and the answer is deliberately narrow:
// delegation exists so a user is not asked about routine work forty times an
// hour, not so that the dangerous cases stop being asked about. A delegation
// rule that covered the dangerous cases would be indistinguishable from having
// no confirmation at all.

// Mode is the operating mode a Goal is being confirmed under.
type Mode string

const (
	ModeStandard Mode = "standard"
	ModeUltra    Mode = "ultra"
)

// DelegationPolicy describes what ULTRA has been permitted to decide.
type DelegationPolicy struct {
	// Entitled reports a verified, unexpired ULTRA entitlement.
	Entitled bool
	// ExecutionEnabled reports that ULTRA Execution is switched on. ULTRA
	// being available is not the same as it being active: with Execution off,
	// ULTRA behaves exactly like Standard.
	ExecutionEnabled bool
}

// Decision is who must confirm a Goal, and why.
type Decision struct {
	// Delegated reports that MARSHAL may proceed without asking.
	Delegated bool `json:"delegated"`
	// RequiresUser reports that a person must decide.
	RequiresUser bool `json:"requires_user"`
	// Reasons explain the decision in the user's terms. They are populated
	// whenever confirmation is required, so a user is never asked to approve
	// something without being told what made it worth asking about.
	Reasons []string `json:"reasons,omitempty"`
	// HardApprovalRequired marks a case delegation can never cover.
	HardApprovalRequired bool `json:"hard_approval_required"`
}

// hardApprovalDimensions are the dimensions no delegation may cover.
//
// These are the effects a person would want to have been asked about even if
// they had switched every convenience on: work that cannot be undone, work
// that reaches outside the project, work touching sensitive data, work
// requiring privilege, and work on something people depend on.
var hardApprovalDimensions = map[string]Level{
	"reversibility":           LevelHigh,
	"external_effects":        LevelHigh,
	"data_sensitivity":        LevelHigh,
	"privilege":               LevelHigh,
	"operational_criticality": LevelHigh,
}

// RequiresHardApproval reports whether an assessment reaches a level that a
// person must approve regardless of mode or policy.
func RequiresHardApproval(assessment Assessment) ([]string, bool) {
	dimensions := assessment.Dimensions()
	var triggered []string
	for name, threshold := range hardApprovalDimensions {
		if dimensions[name].AtLeast(threshold) {
			triggered = append(triggered, name)
		}
	}
	sort.Strings(triggered)
	return triggered, len(triggered) > 0
}

// Apply records a confirmation decision on a Goal.
//
// A delegated decision is recorded as DELEGATED rather than APPROVED, so a
// choice policy made on someone's behalf is never later mistaken for one they
// made themselves. That distinction matters when a result is reviewed: "you
// approved this" and "your settings allowed this" are different claims.
func Apply(intake Intake, decision Decision) Intake {
	switch {
	case decision.RequiresUser:
		if intake.NeedsClarification() {
			intake.Confirmation = ConfirmationNeedsInput
		} else {
			intake.Confirmation = ConfirmationPending
		}
	case decision.Delegated:
		intake.Confirmation = ConfirmationDelegated
	}
	return intake
}

// Approve records a user's acceptance.
func Approve(intake Intake) Intake {
	// A Goal with open questions cannot be approved: approving it would
	// approve whatever MARSHAL happened to guess.
	if intake.NeedsClarification() {
		intake.Confirmation = ConfirmationNeedsInput
		return intake
	}
	intake.Confirmation = ConfirmationApproved
	return intake
}

// ApproveContract applies the Process 03 user-confirmation rule to a durable
// GoalContract. Persistence and CAS remain the application service's job; this
// function is the lifecycle authority for whether the transition is valid.
func ApproveContract(goal model.GoalContract) (model.GoalContract, error) {
	if len(goal.UnresolvedDecisions) > 0 || goal.UnderstandingState == model.GoalNeedsInput {
		return goal, fmt.Errorf("%w: a goal with unresolved decisions cannot be approved", ErrInvalidIntake)
	}
	if goal.Confirmation == model.ConfirmationCancelled {
		return goal, fmt.Errorf("%w: a cancelled goal cannot be approved", ErrInvalidIntake)
	}
	approved := goal
	approved.Confirmation = model.ConfirmationApproved
	return approved, nil
}

// Cancel records a user's refusal.
func Cancel(intake Intake) Intake {
	intake.Confirmation = ConfirmationCancelled
	return intake
}
