package goalintake_test

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/goalintake"
)

func ultraActive() goalintake.DelegationPolicy {
	return goalintake.DelegationPolicy{Entitled: true, ExecutionEnabled: true}
}

// Cancelling stops the work.
func TestCancelStopsTheGoal(t *testing.T) {
	cancelled := goalintake.Cancel(mustForm(t, "add a unit test"))
	if cancelled.Confirmation != goalintake.ConfirmationCancelled {
		t.Fatalf("cancelling produced %s", cancelled.Confirmation)
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
