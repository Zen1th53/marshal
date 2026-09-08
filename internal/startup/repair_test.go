package startup_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/startup"
)

func brokenAssessment() startup.Assessment {
	return startup.Summarize([]startup.Check{
		{
			ID: "project.initialized", Dimension: startup.DimensionProject,
			Status: startup.StatusMissing, Required: true, Reason: startup.ReasonProjectNotInit,
			Summary:      "MARSHAL has not been set up for this project yet.",
			Impact:       "Work cannot run until the project is set up.",
			Capabilities: []startup.Capability{startup.CapProjectExecution},
		},
	}, startup.SummaryOptions{})
}

func fixedAssessment() startup.Assessment {
	return startup.Summarize([]startup.Check{
		{
			ID: "project.initialized", Dimension: startup.DimensionProject,
			Status: startup.StatusReady, Required: true, Reason: startup.ReasonOK,
			Summary: "This project is set up for MARSHAL.",
		},
	}, startup.SummaryOptions{})
}

// Diagnosis explains without acting.
func TestDiagnoseExplainsWithoutRepairing(t *testing.T) {
	diagnoses := startup.Diagnose(brokenAssessment())
	if len(diagnoses) != 1 {
		t.Fatalf("expected one diagnosis, got %d", len(diagnoses))
	}
	diagnosis := diagnoses[0]
	for name, value := range map[string]string{
		"problem": diagnosis.Problem, "why": diagnosis.Why,
		"impact": diagnosis.Impact, "fix": diagnosis.Fix,
	} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("the diagnosis has no %s", name)
		}
	}
	if !diagnosis.NeedsConsent {
		t.Fatal("a repair that mutates the project did not require consent")
	}
	// A healthy assessment has nothing to diagnose.
	if len(startup.Diagnose(fixedAssessment())) != 0 {
		t.Fatal("a healthy assessment produced diagnoses")
	}
}

// Without consent nothing runs, and nothing is reported as fixed.
func TestRepairWithoutConsentDoesNothing(t *testing.T) {
	ran := false
	result := startup.Repair(context.Background(),
		startup.RepairRequest{CheckID: "project.initialized", Reason: startup.ReasonProjectNotInit},
		func(context.Context) error { ran = true; return nil },
		func(context.Context) startup.Assessment { return fixedAssessment() },
	)
	if ran {
		t.Fatal("a repair ran without consent")
	}
	if result.Outcome != startup.RepairRefused || result.Outcome.Fixed() {
		t.Fatalf("outcome was %s, want REFUSED", result.Outcome)
	}
	if result.Retested {
		t.Fatal("an unconsented repair re-tested anyway")
	}
}

// Consent for one condition does not authorize repairing a different one.
func TestConsentDoesNotTransferBetweenConditions(t *testing.T) {
	ran := false
	result := startup.Repair(context.Background(),
		// Consent given, but for a condition MARSHAL does not repair.
		startup.RepairRequest{CheckID: "env.sandbox", Reason: startup.ReasonSandboxUnavailable, Consented: true},
		func(context.Context) error { ran = true; return nil },
		func(context.Context) startup.Assessment { return fixedAssessment() },
	)
	if ran {
		t.Fatal("MARSHAL improvised a repair for a condition it does not fix")
	}
	if result.Outcome != startup.RepairRefused {
		t.Fatalf("outcome was %s, want REFUSED", result.Outcome)
	}
}

// The re-test is what makes Fixed meaningful.
func TestFixedRequiresARetestThatPasses(t *testing.T) {
	result := startup.Repair(context.Background(),
		startup.RepairRequest{CheckID: "project.initialized", Reason: startup.ReasonProjectNotInit, Consented: true},
		func(context.Context) error { return nil },
		func(context.Context) startup.Assessment { return fixedAssessment() },
	)
	if !result.Outcome.Fixed() {
		t.Fatalf("a successful repair with a passing re-test produced %s", result.Outcome)
	}
	if !result.Retested {
		t.Fatal("Fixed was reported without a re-test")
	}
	if result.StatusAfter != startup.StatusReady {
		t.Fatalf("status after repair was %s, want READY", result.StatusAfter)
	}
}

// A repair that "succeeds" but leaves the problem in place is not Fixed. This
// is the case that separates reporting an action from reporting an outcome.
func TestSuccessfulRepairCommandIsNotEnoughToClaimFixed(t *testing.T) {
	result := startup.Repair(context.Background(),
		startup.RepairRequest{CheckID: "project.initialized", Reason: startup.ReasonProjectNotInit, Consented: true},
		// The repair reports success...
		func(context.Context) error { return nil },
		// ...but the re-test still finds the problem.
		func(context.Context) startup.Assessment { return brokenAssessment() },
	)
	if result.Outcome.Fixed() {
		t.Fatal("a repair was reported as Fixed while the problem remained")
	}
	if result.Outcome != startup.RepairAttemptedNotFixed {
		t.Fatalf("outcome was %s, want ATTEMPTED_NOT_FIXED", result.Outcome)
	}
	if !result.Retested {
		t.Fatal("the repair was not re-tested")
	}
	if !strings.Contains(result.Message, "remains") {
		t.Fatalf("the message does not say the problem remains: %q", result.Message)
	}
}

// A repair that errors is reported as failed, and never as fixed. The
// underlying error text stays internal.
func TestFailedRepairIsNotFixedAndLeaksNothing(t *testing.T) {
	result := startup.Repair(context.Background(),
		startup.RepairRequest{CheckID: "project.initialized", Reason: startup.ReasonProjectNotInit, Consented: true},
		func(context.Context) error { return errors.New("exit status 128: fatal: permission denied at 0x7f") },
		func(context.Context) startup.Assessment { return fixedAssessment() },
	)
	if result.Outcome.Fixed() {
		t.Fatal("a failed repair was reported as fixed")
	}
	if result.Outcome != startup.RepairFailed {
		t.Fatalf("outcome was %s, want FAILED", result.Outcome)
	}
	for _, leak := range []string{"exit status", "0x", "fatal:"} {
		if strings.Contains(strings.ToLower(result.Message), leak) {
			t.Fatalf("the repair error leaked internals to the user: %q", result.Message)
		}
	}
}

// A re-test that cannot find the check cannot confirm anything.
func TestUnconfirmableRepairIsNotFixed(t *testing.T) {
	result := startup.Repair(context.Background(),
		startup.RepairRequest{CheckID: "project.initialized", Reason: startup.ReasonProjectNotInit, Consented: true},
		func(context.Context) error { return nil },
		func(context.Context) startup.Assessment {
			return startup.Summarize(nil, startup.SummaryOptions{})
		},
	)
	if result.Outcome.Fixed() {
		t.Fatal("a repair whose result could not be confirmed was reported as fixed")
	}
	if result.StatusAfter != startup.StatusUnknown {
		t.Fatalf("status after an unconfirmable repair was %s, want UNKNOWN", result.StatusAfter)
	}
}

// The set of conditions MARSHAL repairs itself stays deliberately small.
// Installing tools, creating repositories, writing policy and granting
// entitlements are the user's decisions.
func TestMarshalDoesNotRepairTheUsersEnvironment(t *testing.T) {
	forbidden := []startup.ReasonCode{
		startup.ReasonGitMissing, startup.ReasonNotAGitRepository, startup.ReasonRepositoryEmpty,
		startup.ReasonSandboxUnavailable, startup.ReasonNetworkUnenforced,
		startup.ReasonPolicyMissing, startup.ReasonPolicyInvalid,
		startup.ReasonHarnessMissing, startup.ReasonCredentialMissing,
		startup.ReasonUltraNotEntitled, startup.ReasonUltraExpired,
		startup.ReasonStateCorrupt,
	}
	for _, reason := range forbidden {
		ran := false
		result := startup.Repair(context.Background(),
			startup.RepairRequest{CheckID: "x", Reason: reason, Consented: true},
			func(context.Context) error { ran = true; return nil },
			func(context.Context) startup.Assessment { return fixedAssessment() },
		)
		if ran {
			t.Fatalf("MARSHAL attempted to repair %s on the user's behalf", reason)
		}
		if result.Outcome.Fixed() {
			t.Fatalf("%s was reported as fixed without being repaired", reason)
		}
	}
}
