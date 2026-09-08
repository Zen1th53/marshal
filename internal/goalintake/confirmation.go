package goalintake

import "sort"

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

// Confirm decides who must confirm a Goal.
//
// The checks run in order of authority. A hard approval is decided first and
// cannot be reached past: no combination of mode, entitlement and policy
// produces a delegated hard approval, which is what makes ULTRA a capability
// increase rather than a safety decrease.
func Confirm(intake Intake, mode Mode, policy DelegationPolicy) Decision {
	decision := Decision{}

	// An unanswered question cannot be delegated: there is nothing to delegate
	// to, because MARSHAL does not know what was wanted.
	if intake.NeedsClarification() {
		decision.RequiresUser = true
		decision.Reasons = append(decision.Reasons, "The request needs clarification before work can be planned.")
		return decision
	}

	// Hard approvals are decided before mode is consulted at all, so the
	// delegation path never has an opportunity to cover one.
	if triggered, required := RequiresHardApproval(intake.Assessment); required {
		decision.RequiresUser = true
		decision.HardApprovalRequired = true
		for _, dimension := range triggered {
			decision.Reasons = append(decision.Reasons, hardApprovalReason(dimension))
		}
		return decision
	}

	// A dimension nobody could establish is not delegable either. Delegation
	// means acting on someone's behalf within limits they agreed to, and that
	// requires knowing what the work reaches. An unestablished dimension is
	// precisely the case where nobody knows, so it goes back to the person.
	if unestablished := intake.Assessment.Unestablished(); len(unestablished) > 0 {
		decision.RequiresUser = true
		for _, dimension := range unestablished {
			decision.Reasons = append(decision.Reasons,
				"MARSHAL could not establish this request's "+readable(dimension)+".")
		}
		return decision
	}

	// Below the hard-approval line, and with everything established, ULTRA
	// with Execution on may proceed. Entitlement and Execution are both
	// required: an entitlement grants the capability, and Execution is the
	// user actually switching it on.
	if mode == ModeUltra && policy.Entitled && policy.ExecutionEnabled {
		decision.Delegated = true
		return decision
	}

	// Standard, and ULTRA with Execution off, ask about anything the
	// assessment flagged. Everything else proceeds without a prompt.
	if intake.Assessment.RequiresConfirmation() {
		decision.RequiresUser = true
		for _, dimension := range intake.Assessment.Elevated() {
			decision.Reasons = append(decision.Reasons, elevatedReason(dimension))
		}
		for _, dimension := range intake.Assessment.Unestablished() {
			decision.Reasons = append(decision.Reasons,
				"MARSHAL could not establish this request's "+readable(dimension)+".")
		}
		return decision
	}

	decision.Delegated = true
	return decision
}

func hardApprovalReason(dimension string) string {
	switch dimension {
	case "reversibility":
		return "This cannot be undone automatically."
	case "external_effects":
		return "This affects systems outside the project."
	case "data_sensitivity":
		return "This involves sensitive data."
	case "privilege":
		return "This needs elevated access."
	case "operational_criticality":
		return "This affects a system people depend on."
	default:
		return "This needs your approval."
	}
}

func elevatedReason(dimension string) string {
	switch dimension {
	case "blast_radius":
		return "This reaches a large part of the project."
	case "privilege":
		return "This needs additional access."
	case "reversibility":
		return "Undoing this would be difficult."
	case "external_effects":
		return "This reaches outside the project."
	case "data_sensitivity":
		return "This involves data worth being careful with."
	case "operational_criticality":
		return "This affects something in active use."
	default:
		return "This is worth a look before it runs."
	}
}

func readable(dimension string) string {
	switch dimension {
	case "blast_radius":
		return "reach"
	case "operational_criticality":
		return "importance"
	case "verification_difficulty":
		return "verifiability"
	case "dependency_depth":
		return "dependencies"
	default:
		return dimension
	}
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

// Cancel records a user's refusal.
func Cancel(intake Intake) Intake {
	intake.Confirmation = ConfirmationCancelled
	return intake
}
