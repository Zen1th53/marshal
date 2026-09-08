package startup

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// This file implements the Setup/Doctor/repair distinction.
//
//	Setup   assesses readiness and changes nothing.
//	Doctor  diagnoses: it explains what is wrong, why it matters, and what
//	        would fix it. It still changes nothing.
//	Repair  mutates, and only ever with explicit consent for that specific
//	        repair, after which it re-runs the original check and reports the
//	        real outcome.
//
// The re-test is the part that makes "Fixed" mean anything. A repair that
// reports success because the repair command exited zero is asserting that an
// action was taken, not that the problem is gone. Only re-running the check
// that failed can establish the latter, so Fixed is unreachable without it.

// Diagnosis explains one problem and what could be done about it.
type Diagnosis struct {
	// CheckID names the check this diagnosis is about.
	CheckID string `json:"check_id"`
	// Problem is what is wrong, in the user's terms.
	Problem string `json:"problem"`
	// Why explains why it matters.
	Why string `json:"why"`
	// Impact says what is unavailable because of it.
	Impact string `json:"impact"`
	// Fix describes the proposed repair. It is a description, not an action.
	Fix string `json:"fix,omitempty"`
	// Repairable reports whether MARSHAL can perform the fix itself.
	Repairable bool `json:"repairable"`
	// NeedsConsent reports whether the fix mutates the user's machine or
	// project. Every such fix must be asked for individually.
	NeedsConsent bool `json:"needs_consent"`
	// Reason is the stable machine code.
	Reason ReasonCode `json:"reason"`
}

// Diagnose explains everything wrong with an assessment. It performs no
// repairs and takes no arguments that could authorize one.
func Diagnose(assessment Assessment) []Diagnosis {
	var diagnoses []Diagnosis
	for _, check := range assessment.Checks {
		if check.Status.Healthy() || check.Status == StatusOptional {
			continue
		}
		info := Describe(check.Reason)
		diagnosis := Diagnosis{
			CheckID:      check.ID,
			Problem:      check.Summary,
			Why:          whyItMatters(check),
			Impact:       check.Impact,
			Fix:          info.Remedy,
			Repairable:   repairableReasons[check.Reason],
			NeedsConsent: info.RepairNeedsConsent || repairableReasons[check.Reason],
			Reason:       check.Reason,
		}
		diagnoses = append(diagnoses, diagnosis)
	}
	sort.SliceStable(diagnoses, func(a, b int) bool { return diagnoses[a].CheckID < diagnoses[b].CheckID })
	return diagnoses
}

func whyItMatters(check Check) string {
	switch check.Dimension {
	case DimensionCore:
		return "MARSHAL needs this to keep track of your work."
	case DimensionProject:
		return "This is needed to work on a project safely."
	case DimensionEnvironment:
		return "This is part of running work safely on this machine."
	default:
		return "This affects what MARSHAL can do."
	}
}

// repairableReasons are the conditions MARSHAL can fix itself.
//
// The list is deliberately short. Everything absent from it is something
// MARSHAL will explain but not do: creating a Git repository, installing a
// dependency, writing a capability policy, or granting an entitlement are all
// decisions belonging to the user, and performing them because a check failed
// would be exactly the silent repair Process 01 forbids.
var repairableReasons = map[ReasonCode]bool{
	ReasonProjectNotInit: true,
}

// RepairRequest asks for one specific repair.
type RepairRequest struct {
	// CheckID is the check whose failure is being repaired.
	CheckID string
	// Reason is the condition being repaired. It must match what was
	// diagnosed, so consent given for one problem cannot be applied to a
	// different one discovered later.
	Reason ReasonCode
	// Consented must be true. It is a separate field rather than an implicit
	// property of calling Repair so that consent is something a caller has to
	// state, not something it acquires by invoking the function.
	Consented bool
}

// RepairOutcome is the result of an attempted repair.
type RepairOutcome string

const (
	// RepairFixed means the repair ran and the re-test then passed. It is the
	// only outcome that may be shown as success.
	RepairFixed RepairOutcome = "FIXED"
	// RepairAttemptedNotFixed means the repair ran and the re-test still
	// failed. The action happened; the problem did not go away.
	RepairAttemptedNotFixed RepairOutcome = "ATTEMPTED_NOT_FIXED"
	// RepairRefused means the repair was not attempted.
	RepairRefused RepairOutcome = "REFUSED"
	// RepairFailed means the repair itself errored.
	RepairFailed RepairOutcome = "FAILED"
)

// Fixed reports whether the problem is actually gone.
func (o RepairOutcome) Fixed() bool { return o == RepairFixed }

// RepairResult reports what happened, and what the re-test found.
type RepairResult struct {
	CheckID string        `json:"check_id"`
	Outcome RepairOutcome `json:"outcome"`
	// Message is user-facing.
	Message string `json:"message"`
	// Retested reports whether the original check was re-run. A result with
	// this false can never be Fixed.
	Retested bool `json:"retested"`
	// StatusAfter is what the re-test found.
	StatusAfter Status    `json:"status_after"`
	CompletedAt time.Time `json:"completed_at"`
}

// RepairFunc performs a repair. It returns an error if the repair itself could
// not be carried out.
type RepairFunc func(ctx context.Context) error

// Repair performs a consented repair and verifies the result.
//
// Consent is checked first and is specific to the diagnosed condition, so a
// caller cannot carry consent given for one problem onto another. After the
// repair runs, the original check is re-run through the supplied reassess
// function, and the outcome reflects what that found rather than whether the
// repair command succeeded.
func Repair(ctx context.Context, request RepairRequest, fix RepairFunc, reassess func(context.Context) Assessment) RepairResult {
	result := RepairResult{CheckID: request.CheckID, CompletedAt: time.Now().UTC()}

	if !request.Consented {
		result.Outcome = RepairRefused
		result.Message = "No change was made. This repair needs your confirmation first."
		return result
	}
	if !repairableReasons[request.Reason] {
		// Refusing here rather than attempting something plausible is the
		// point: MARSHAL does not improvise fixes for conditions it was not
		// designed to repair.
		result.Outcome = RepairRefused
		result.Message = "MARSHAL does not repair this automatically. The suggested fix is yours to apply."
		return result
	}
	if fix == nil || reassess == nil {
		result.Outcome = RepairFailed
		result.Message = "This repair is unavailable."
		return result
	}

	if err := fix(ctx); err != nil {
		result.Outcome = RepairFailed
		// The underlying error is deliberately not surfaced: it is an internal
		// detail, and the user needs to know the state of their machine rather
		// than the text of a failure.
		result.Message = "The repair could not be completed. Nothing was reported as fixed."
		return result
	}

	// The re-test decides the outcome. Without it there is no basis for
	// claiming the problem is gone.
	after := reassess(ctx)
	result.Retested = true
	check, found := after.Check(request.CheckID)
	if !found {
		result.Outcome = RepairAttemptedNotFixed
		result.StatusAfter = StatusUnknown
		result.Message = "The repair ran, but the result could not be confirmed."
		return result
	}
	result.StatusAfter = check.Status
	if check.Status.Healthy() {
		result.Outcome = RepairFixed
		result.Message = fmt.Sprintf("Fixed. %s", check.Summary)
		return result
	}
	result.Outcome = RepairAttemptedNotFixed
	result.Message = fmt.Sprintf("The repair ran but the problem remains. %s", check.Summary)
	return result
}
