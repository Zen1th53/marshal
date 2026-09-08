package startup_test

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/startup"
)

func check(id string, dimension startup.Dimension, status startup.Status, required bool, reason startup.ReasonCode, caps ...startup.Capability) startup.Check {
	return startup.Check{
		ID: id, Dimension: dimension, Status: status, Required: required,
		Reason: reason, Summary: id + " observed", Capabilities: caps,
	}
}

func healthyChecks() []startup.Check {
	return []startup.Check{
		check("core.state", startup.DimensionCore, startup.StatusReady, true, startup.ReasonOK),
		check("project.git", startup.DimensionProject, startup.StatusReady, true, startup.ReasonOK, startup.CapProjectExecution),
		check("env.sandbox", startup.DimensionEnvironment, startup.StatusReady, true, startup.ReasonOK, startup.CapProjectExecution),
	}
}

func TestHealthyAssessmentIsReadyAndExecutable(t *testing.T) {
	assessment := startup.Summarize(healthyChecks(), startup.SummaryOptions{})
	if assessment.Phase != startup.PhaseReady {
		t.Fatalf("a healthy environment produced %s, want READY", assessment.Phase)
	}
	if !assessment.ControlCenterOpens() || !assessment.ExecutionPermitted() {
		t.Fatal("a healthy environment did not permit both the control center and execution")
	}
}

// The primary invariant of Process 01: every failure short of a core failure
// still opens the control center.
func TestControlCenterOpensDespiteEveryNonCoreFailure(t *testing.T) {
	failures := map[string]startup.Check{
		"no git":             check("project.git", startup.DimensionProject, startup.StatusMissing, true, startup.ReasonGitMissing, startup.CapProjectExecution),
		"not a repository":   check("project.repo", startup.DimensionProject, startup.StatusMissing, true, startup.ReasonNotAGitRepository, startup.CapProjectExecution),
		"repository empty":   check("project.head", startup.DimensionProject, startup.StatusNeedsAttention, true, startup.ReasonRepositoryEmpty, startup.CapProjectExecution),
		"not initialized":    check("project.init", startup.DimensionProject, startup.StatusMissing, true, startup.ReasonProjectNotInit, startup.CapProjectExecution),
		"project moved":      check("project.identity", startup.DimensionProject, startup.StatusNeedsAttention, true, startup.ReasonProjectMoved, startup.CapProjectExecution),
		"no sandbox":         check("env.sandbox", startup.DimensionEnvironment, startup.StatusMissing, true, startup.ReasonSandboxUnavailable, startup.CapProjectExecution),
		"network unenforced": check("env.network", startup.DimensionEnvironment, startup.StatusBroken, true, startup.ReasonNetworkUnenforced, startup.CapNetworkEgress),
		"policy missing":     check("env.policy", startup.DimensionEnvironment, startup.StatusMissing, true, startup.ReasonPolicyMissing, startup.CapProjectExecution),
		"policy invalid":     check("env.policy", startup.DimensionEnvironment, startup.StatusBroken, true, startup.ReasonPolicyInvalid, startup.CapProjectExecution),
		"harness missing":    check("env.harness", startup.DimensionEnvironment, startup.StatusMissing, false, startup.ReasonHarnessMissing, startup.CapProviderExecution),
		"state corrupt":      check("core.state", startup.DimensionCore, startup.StatusBroken, true, startup.ReasonStateCorrupt, startup.CapProjectExecution),
		"schema too new":     check("core.schema", startup.DimensionCore, startup.StatusBroken, true, startup.ReasonSchemaTooNew, startup.CapProjectExecution),
		"migration failed":   check("core.migration", startup.DimensionCore, startup.StatusBroken, true, startup.ReasonMigrationFailed, startup.CapProjectExecution),
		"offline":            check("env.network", startup.DimensionEnvironment, startup.StatusLimited, false, startup.ReasonOffline, startup.CapNetworkEgress),
		"unknown check":      check("env.unknown", startup.DimensionEnvironment, startup.StatusUnknown, true, startup.ReasonNotChecked, startup.CapProjectExecution),
	}
	for name, failing := range failures {
		t.Run(name, func(t *testing.T) {
			assessment := startup.Summarize([]startup.Check{failing}, startup.SummaryOptions{})
			if !assessment.ControlCenterOpens() {
				t.Fatalf("%s prevented the control center from opening (phase %s)", name, assessment.Phase)
			}
			// The surfaces used to diagnose and repair must survive.
			for _, capability := range startup.AlwaysAvailable() {
				if !assessment.Has(capability) {
					t.Fatalf("%s removed %s, which is needed to fix it", name, capability)
				}
			}
		})
	}
}

// Only a genuine core failure closes the control center, and the set of
// conditions that can do so is deliberately tiny.
func TestOnlyCoreFailureClosesControlCenter(t *testing.T) {
	fatal := check("core.statedir", startup.DimensionCore, startup.StatusBroken, true, startup.ReasonStateDirUnwritable)
	assessment := startup.Summarize([]startup.Check{fatal}, startup.SummaryOptions{})
	if assessment.Phase != startup.PhaseCoreFailed {
		t.Fatalf("an unwritable state directory produced %s, want CORE_FAILED", assessment.Phase)
	}
	if assessment.ControlCenterOpens() {
		t.Fatal("the control center opened despite a core failure")
	}

	// Every other reason code must be non-fatal to the control center.
	for _, code := range startup.KnownReasonCodes() {
		info := startup.Describe(code)
		if info.BlocksControlCenter && !startup.CoreFatal(code) {
			t.Fatalf("reason %s blocks the control center but is not a core-fatal condition", code)
		}
	}
}

// UNKNOWN is never Ready. A check that could not run tells you nothing.
func TestUnknownIsNeverHealthy(t *testing.T) {
	if startup.StatusUnknown.Healthy() {
		t.Fatal("UNKNOWN was treated as healthy")
	}
	if !startup.StatusUnknown.Blocking() {
		t.Fatal("UNKNOWN did not block a required check")
	}
	unknown := check("env.sandbox", startup.DimensionEnvironment, startup.StatusUnknown, true, startup.ReasonNotChecked, startup.CapProjectExecution)
	assessment := startup.Summarize([]startup.Check{unknown}, startup.SummaryOptions{})
	if assessment.ExecutionPermitted() {
		t.Fatal("execution was permitted on an unchecked required prerequisite")
	}
	if assessment.Has(startup.CapProjectExecution) {
		t.Fatal("an unchecked prerequisite left its capability enabled")
	}
	// Not having checked is not a reason to hide the control center.
	if !assessment.ControlCenterOpens() {
		t.Fatal("an unchecked prerequisite closed the control center")
	}
}

// A missing optional capability disables only itself.
func TestOptionalFailureIsScoped(t *testing.T) {
	checks := append(healthyChecks(),
		check("env.harness", startup.DimensionEnvironment, startup.StatusMissing, false, startup.ReasonHarnessMissing, startup.CapProviderExecution))
	assessment := startup.Summarize(checks, startup.SummaryOptions{})

	if assessment.Has(startup.CapProviderExecution) {
		t.Fatal("a missing harness left provider execution enabled")
	}
	if !assessment.Has(startup.CapProjectExecution) {
		t.Fatal("a missing optional harness disabled unrelated project execution")
	}
	if assessment.Phase == startup.PhaseExecutionBlocked {
		t.Fatal("a non-required missing harness blocked execution entirely")
	}
	if !assessment.ControlCenterOpens() {
		t.Fatal("a missing optional harness closed the control center")
	}
}

// A blocking failure disables execution but leaves the repair surfaces intact.
func TestBlockedExecutionPreservesRepairSurfaces(t *testing.T) {
	blocked := check("env.sandbox", startup.DimensionEnvironment, startup.StatusMissing, true, startup.ReasonSandboxUnavailable, startup.CapProjectExecution)
	assessment := startup.Summarize([]startup.Check{blocked}, startup.SummaryOptions{})

	if assessment.Phase != startup.PhaseExecutionBlocked {
		t.Fatalf("phase was %s, want EXECUTION_BLOCKED", assessment.Phase)
	}
	if assessment.ExecutionPermitted() {
		t.Fatal("execution was permitted with no sandbox")
	}
	for _, capability := range []startup.Capability{
		startup.CapSetup, startup.CapDoctor, startup.CapHelp,
		startup.CapInspect, startup.CapHistory, startup.CapRecovery,
	} {
		if !assessment.Has(capability) {
			t.Fatalf("%s was removed by a blocked execution state", capability)
		}
	}
	if len(assessment.Blocking()) != 1 {
		t.Fatalf("expected exactly one blocking check, got %d", len(assessment.Blocking()))
	}
}

// ULTRA is never on by default and never enabled by a failing environment.
func TestUltraIsNeverDefaultOn(t *testing.T) {
	assessment := startup.Summarize(healthyChecks(), startup.SummaryOptions{})
	if assessment.Has(startup.CapUltra) {
		t.Fatal("ULTRA was available without an entitlement")
	}
	entitled := startup.Summarize(healthyChecks(), startup.SummaryOptions{UltraEntitled: true})
	if !entitled.Has(startup.CapUltra) {
		t.Fatal("a verified entitlement did not enable ULTRA")
	}
	// An entitlement does not rescue a blocked environment.
	blocked := append(healthyChecks(),
		check("env.sandbox2", startup.DimensionEnvironment, startup.StatusMissing, true, startup.ReasonSandboxUnavailable, startup.CapProjectExecution))
	ultraBlocked := startup.Summarize(blocked, startup.SummaryOptions{UltraEntitled: true})
	if ultraBlocked.ExecutionPermitted() {
		t.Fatal("ULTRA permitted execution in an environment that blocks Standard")
	}
}

// Interrupted work is surfaced for a decision, never resumed on its own.
func TestRecoveryIsOfferedNotTaken(t *testing.T) {
	assessment := startup.Summarize(healthyChecks(), startup.SummaryOptions{RecoveryAvailable: true})
	if assessment.Phase != startup.PhaseRecoveryAvailable {
		t.Fatalf("phase was %s, want RECOVERY_AVAILABLE", assessment.Phase)
	}
	if assessment.ExecutionPermitted() {
		t.Fatal("interrupted work was resumed automatically instead of being offered")
	}
	if !assessment.Has(startup.CapRecovery) {
		t.Fatal("recovery was unavailable in the recovery phase")
	}
}

// Raw internal errors must never reach a user-facing summary.
func TestRawInternalErrorsAreRejected(t *testing.T) {
	leaky := []string{
		"git [rev-parse --show-toplevel]: exit status 128: fatal: not a git repository",
		`configure SQLite with "PRAGMA foreign_keys = ON": unable to open database file (14)`,
		"panic: runtime error: invalid memory address",
		"goroutine 1 [running]:",
		"",
	}
	for _, summary := range leaky {
		assessment := startup.Summarize([]startup.Check{{
			ID: "leaky", Dimension: startup.DimensionCore, Status: startup.StatusBroken,
			Reason: startup.ReasonCheckError, Summary: summary,
		}}, startup.SummaryOptions{})
		safe, offenders := assessment.UserSafe()
		if safe {
			t.Fatalf("a raw internal error passed the user-safety check: %q", summary)
		}
		if len(offenders) != 1 || offenders[0] != "leaky" {
			t.Fatalf("the offending check was not identified: %v", offenders)
		}
	}

	clean := startup.Summarize(healthyChecks(), startup.SummaryOptions{})
	if safe, offenders := clean.UserSafe(); !safe {
		t.Fatalf("clean summaries were rejected: %v", offenders)
	}
}

// Assessment is deterministic and stably ordered, so two surfaces rendering
// the same state cannot disagree about the order or the outcome.
func TestAssessmentIsDeterministic(t *testing.T) {
	checks := append(healthyChecks(),
		check("z.last", startup.DimensionEnvironment, startup.StatusLimited, false, startup.ReasonOffline),
		check("a.first", startup.DimensionEnvironment, startup.StatusReady, false, startup.ReasonOK))
	first := startup.Summarize(checks, startup.SummaryOptions{Now: time.Unix(0, 0).UTC()})
	for i := 0; i < 10; i++ {
		next := startup.Summarize(checks, startup.SummaryOptions{Now: time.Unix(0, 0).UTC()})
		if next.Phase != first.Phase || len(next.Capabilities) != len(first.Capabilities) {
			t.Fatal("repeated assessment of identical input differed")
		}
		for j := range next.Checks {
			if next.Checks[j].ID != first.Checks[j].ID {
				t.Fatal("check ordering is not stable")
			}
		}
	}
	if first.Checks[0].ID != "a.first" {
		t.Fatalf("checks are not sorted by ID: first is %q", first.Checks[0].ID)
	}
}

// Every catalogued reason carries a remedy, and consent-requiring repairs are
// marked as such so no surface can perform one silently.
func TestReasonCatalogIsComplete(t *testing.T) {
	for _, code := range startup.KnownReasonCodes() {
		info := startup.Describe(code)
		if info.Remedy == "" {
			t.Fatalf("reason %s has no remedy", code)
		}
		if info.Code != code {
			t.Fatalf("reason %s is mis-keyed in the catalog", code)
		}
	}
	// An unknown code fails closed on execution but not on the control center.
	unknown := startup.Describe(startup.ReasonCode("STARTUP_INVENTED"))
	if !unknown.BlocksExecution {
		t.Fatal("an unrecognised startup condition permitted execution")
	}
	if unknown.BlocksControlCenter {
		t.Fatal("an unrecognised startup condition hid the control center that would explain it")
	}
}

// Conditions whose repair changes the user's machine or project must be
// flagged, because Process 01 forbids silent repair.
func TestMutatingRepairsRequireConsent(t *testing.T) {
	for _, code := range []startup.ReasonCode{
		startup.ReasonStateDirUnwritable, startup.ReasonStateCorrupt, startup.ReasonMigrationFailed,
	} {
		if !startup.Describe(code).RepairNeedsConsent {
			t.Fatalf("repairing %s does not require consent", code)
		}
	}
}
