package goalintake_test

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/goalintake"
)

func ultraActive() goalintake.DelegationPolicy {
	return goalintake.DelegationPolicy{Entitled: true, ExecutionEnabled: true}
}

// Routine work proceeds without interrupting anyone, in either mode.
func TestRoutineWorkNeedsNoConfirmation(t *testing.T) {
	intake := mustForm(t, "add a unit test for the config parser")

	standard := goalintake.Confirm(intake, goalintake.ModeStandard, goalintake.DelegationPolicy{})
	if standard.RequiresUser {
		t.Fatalf("routine work required confirmation in Standard: %v", standard.Reasons)
	}
	if !standard.Delegated {
		t.Fatal("routine work was neither delegated nor confirmed")
	}
}

// Standard asks about anything the assessment flagged, and says why.
func TestStandardAsksAboutFlaggedWork(t *testing.T) {
	intake := mustForm(t, "rename the user table across every service")

	decision := goalintake.Confirm(intake, goalintake.ModeStandard, goalintake.DelegationPolicy{})
	if !decision.RequiresUser {
		t.Fatal("a wide-reaching change did not require confirmation in Standard")
	}
	if len(decision.Reasons) == 0 {
		t.Fatal("the user is being asked to approve something with no reason given")
	}
}

// The central ULTRA rule: delegation covers ordinary work and never reaches a
// hard approval.
func TestUltraDelegatesOrdinaryWorkButNeverHardApprovals(t *testing.T) {
	ordinary := mustForm(t, "rename the user table across every service")
	decision := goalintake.Confirm(ordinary, goalintake.ModeUltra, ultraActive())
	if !decision.Delegated {
		t.Fatalf("ULTRA did not delegate ordinary work: %v", decision.Reasons)
	}

	// Every hard-approval case must be refused delegation, in every mode and
	// under every policy.
	hardCases := map[string]string{
		"irreversible": "delete the old records permanently",
		"external":     "deploy the release and notify users",
		"sensitive":    "export the customer pii to a report",
		"privileged":   "run the migration as root",
		"critical":     "restart the production service",
	}
	for name, request := range hardCases {
		t.Run(name, func(t *testing.T) {
			intake := mustForm(t, request)
			decision := goalintake.Confirm(intake, goalintake.ModeUltra, ultraActive())

			if decision.Delegated {
				t.Fatalf("ULTRA delegated a hard approval (%s)", name)
			}
			if !decision.RequiresUser {
				t.Fatalf("a hard approval (%s) did not require a person", name)
			}
			if !decision.HardApprovalRequired {
				t.Fatalf("a hard approval (%s) was not marked as one", name)
			}
			if len(decision.Reasons) == 0 {
				t.Fatalf("a hard approval (%s) gave no reason", name)
			}
		})
	}
}

// ULTRA being available is not the same as it being switched on.
func TestUltraWithExecutionOffBehavesLikeStandard(t *testing.T) {
	intake := mustForm(t, "rename the user table across every service")

	standard := goalintake.Confirm(intake, goalintake.ModeStandard, goalintake.DelegationPolicy{})
	executionOff := goalintake.Confirm(intake, goalintake.ModeUltra,
		goalintake.DelegationPolicy{Entitled: true, ExecutionEnabled: false})

	if executionOff.Delegated != standard.Delegated || executionOff.RequiresUser != standard.RequiresUser {
		t.Fatal("ULTRA with Execution off behaved differently from Standard")
	}
	if !executionOff.RequiresUser {
		t.Fatal("ULTRA with Execution off delegated work Standard would have asked about")
	}
}

// An entitlement alone does not delegate, and neither does Execution alone.
func TestDelegationRequiresBothEntitlementAndExecution(t *testing.T) {
	intake := mustForm(t, "rename the user table across every service")

	for name, policy := range map[string]goalintake.DelegationPolicy{
		"neither":          {},
		"entitlement only": {Entitled: true},
		"execution only":   {ExecutionEnabled: true},
	} {
		t.Run(name, func(t *testing.T) {
			if goalintake.Confirm(intake, goalintake.ModeUltra, policy).Delegated {
				t.Fatalf("delegation happened with %s", name)
			}
		})
	}
	if !goalintake.Confirm(intake, goalintake.ModeUltra, ultraActive()).Delegated {
		t.Fatal("a fully entitled and enabled ULTRA did not delegate ordinary work")
	}
}

// An unanswered question cannot be delegated: there is nothing to delegate to.
func TestOpenQuestionsAreNeverDelegated(t *testing.T) {
	unclear := mustForm(t, "delete the old stuff from production, or something")
	decision := goalintake.Confirm(unclear, goalintake.ModeUltra, ultraActive())

	if decision.Delegated {
		t.Fatal("ULTRA delegated a Goal that MARSHAL did not understand")
	}
	if !decision.RequiresUser {
		t.Fatal("an unanswered question did not require a person")
	}
}

// A delegated decision is recorded distinctly from one a person made, so the
// two are never confused when a result is reviewed later.
func TestDelegatedIsRecordedDistinctlyFromApproved(t *testing.T) {
	intake := mustForm(t, "add a unit test for the config parser")

	delegated := goalintake.Apply(intake, goalintake.Confirm(intake, goalintake.ModeUltra, ultraActive()))
	if delegated.Confirmation != goalintake.ConfirmationDelegated {
		t.Fatalf("delegated work was recorded as %s", delegated.Confirmation)
	}
	if delegated.Confirmation == goalintake.ConfirmationApproved {
		t.Fatal("a delegated decision is indistinguishable from one a person made")
	}
	if !delegated.Confirmation.Settled() {
		t.Fatal("a legitimately delegated Goal did not permit planning")
	}

	approved := goalintake.Approve(intake)
	if approved.Confirmation != goalintake.ConfirmationApproved {
		t.Fatalf("a user approval was recorded as %s", approved.Confirmation)
	}
}

// Cancelling stops the work.
func TestCancelStopsTheGoal(t *testing.T) {
	cancelled := goalintake.Cancel(mustForm(t, "add a unit test"))
	if cancelled.Confirmation != goalintake.ConfirmationCancelled {
		t.Fatalf("cancelling produced %s", cancelled.Confirmation)
	}
	if cancelled.Confirmation.Settled() {
		t.Fatal("a cancelled Goal permitted planning")
	}
}

// A dimension nobody could establish is a reason to ask, not to proceed.
func TestUnestablishedDimensionRequiresAsking(t *testing.T) {
	context := safeContext()
	context.ScopeKnown = false

	intake, err := goalintake.Form(goalintake.FormationRequest{
		Request:   "update the shared configuration",
		ProjectID: testProject,
		SessionID: "SESSION-1",
		Context:   context,
	})
	if err != nil {
		t.Fatalf("form: %v", err)
	}

	decision := goalintake.Confirm(intake, goalintake.ModeUltra, ultraActive())
	if decision.Delegated {
		t.Fatal("ULTRA delegated work whose reach could not be established")
	}
	if len(decision.Reasons) == 0 {
		t.Fatal("no reason was given for asking")
	}
}

// The hard-approval set is stable and covers exactly the effects a person
// would want to have been asked about whatever their settings.
func TestHardApprovalCoversTheDangerousEffects(t *testing.T) {
	for name, request := range map[string]string{
		"irreversible": "permanently destroy the archived data",
		"external":     "publish the package and charge the customer",
		"sensitive":    "read the private key and the medical records",
		"privileged":   "chmod 777 the config and disable auth",
		"critical":     "take the production service down",
	} {
		t.Run(name, func(t *testing.T) {
			intake := mustForm(t, request)
			triggered, required := goalintake.RequiresHardApproval(intake.Assessment)
			if !required {
				t.Fatalf("%q did not trigger a hard approval", request)
			}
			if len(triggered) == 0 {
				t.Fatal("a hard approval was required but no dimension was named")
			}
		})
	}

	// Ordinary work does not trigger one, or the category would mean nothing.
	routine := mustForm(t, "add a unit test for the parser")
	if _, required := goalintake.RequiresHardApproval(routine.Assessment); required {
		t.Fatal("routine work triggered a hard approval")
	}
}
