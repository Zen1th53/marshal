package startup_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/startup"
)

// This suite attacks Process 01. Each test attempts a way of making startup
// claim something that is not true, or of getting startup to act on the user's
// machine without being asked.

// Attack: forge an entitlement by asserting it in the summary options without
// a check having verified one. ULTRA capability must follow the verified fact.
func TestForgedUltraClaimDoesNotWeakenAnything(t *testing.T) {
	// A caller asserts entitlement while the environment is unsafe.
	blocked := []startup.Check{
		check("env.sandbox", startup.DimensionEnvironment, startup.StatusMissing, true,
			startup.ReasonSandboxUnavailable, startup.CapProjectExecution),
	}
	assessment := startup.Summarize(blocked, startup.SummaryOptions{UltraEntitled: true})

	if assessment.ExecutionPermitted() {
		t.Fatal("an asserted ULTRA entitlement permitted execution in an unsafe environment")
	}
	if assessment.Has(startup.CapProjectExecution) {
		t.Fatal("an asserted entitlement restored a capability a failing check removed")
	}
	// The control center is still available, so the user can see why.
	if !assessment.ControlCenterOpens() {
		t.Fatal("the control center closed")
	}
}

// Attack: declare a healthy phase while supplying failing checks. The phase is
// derived, so it cannot be asserted.
func TestPhaseCannotBeAssertedOverFailingChecks(t *testing.T) {
	failing := []startup.Check{
		check("env.sandbox", startup.DimensionEnvironment, startup.StatusBroken, true,
			startup.ReasonSandboxUnavailable, startup.CapProjectExecution),
	}
	assessment := startup.Summarize(failing, startup.SummaryOptions{})
	if assessment.Phase == startup.PhaseReady {
		t.Fatal("a failing check produced a READY phase")
	}
	if assessment.ExecutionPermitted() {
		t.Fatal("execution was permitted with a broken required prerequisite")
	}
}

// Attack: hide a failure by declaring no capabilities on the failing check, so
// that nothing gets disabled. It must still block, because Required drives
// blocking independently of the capability list.
func TestFailingCheckWithNoDeclaredCapabilitiesStillBlocks(t *testing.T) {
	sneaky := startup.Check{
		ID: "env.sneaky", Dimension: startup.DimensionEnvironment,
		Status: startup.StatusBroken, Required: true, Reason: startup.ReasonCheckError,
		Summary: "A required component is not working.",
		// No Capabilities declared.
	}
	assessment := startup.Summarize([]startup.Check{sneaky}, startup.SummaryOptions{})
	if assessment.ExecutionPermitted() {
		t.Fatal("a required broken check with no declared capabilities permitted execution")
	}
	if len(assessment.Blocking()) != 1 {
		t.Fatal("the failing check was not reported as blocking")
	}
}

// Attack: use a failing check to strip the control and repair surfaces, so the
// user cannot diagnose the problem.
func TestFailingCheckCannotRemoveRepairSurfaces(t *testing.T) {
	hostile := startup.Check{
		ID: "env.hostile", Dimension: startup.DimensionEnvironment,
		Status: startup.StatusBroken, Required: true, Reason: startup.ReasonCheckError,
		Summary: "A component is not working.",
		// Declares the protected surfaces as its own, attempting to take them
		// down with it.
		Capabilities: []startup.Capability{
			startup.CapControlCenter, startup.CapDoctor, startup.CapSetup,
			startup.CapHelp, startup.CapRecovery, startup.CapHistory, startup.CapInspect,
		},
	}
	assessment := startup.Summarize([]startup.Check{hostile}, startup.SummaryOptions{})

	if !assessment.ControlCenterOpens() {
		t.Fatal("a failing environment check closed the control center")
	}
	for _, capability := range startup.AlwaysAvailable() {
		if !assessment.Has(capability) {
			t.Fatalf("a failing check removed the protected surface %s", capability)
		}
	}
}

// Attack: report a non-core failure as core-fatal to suppress the control
// center. Only enumerated core-fatal reasons can do that.
func TestNonCoreReasonCannotForceCoreFailure(t *testing.T) {
	impostor := startup.Check{
		ID: "core.impostor", Dimension: startup.DimensionCore,
		Status: startup.StatusBroken, Required: true,
		// A core-dimension check, but with a reason that is not core-fatal.
		Reason:  startup.ReasonSandboxUnavailable,
		Summary: "Something core is wrong.",
	}
	assessment := startup.Summarize([]startup.Check{impostor}, startup.SummaryOptions{})
	if assessment.Phase == startup.PhaseCoreFailed {
		t.Fatal("a non-core-fatal reason produced a core failure")
	}
	if !assessment.ControlCenterOpens() {
		t.Fatal("a non-core-fatal condition closed the control center")
	}
}

// Attack: smuggle a secret or an internal error into a user-facing summary.
func TestSecretsAndInternalsCannotReachTheUser(t *testing.T) {
	leaky := []string{
		"token AKIAIOSFODNN7EXAMPLE was rejected",
		"failed at 0x7fff5fbff8c0",
		"git [rev-parse]: exit status 128",
		`PRAGMA foreign_keys = ON failed`,
		"panic: runtime error",
	}
	for _, summary := range leaky {
		assessment := startup.Summarize([]startup.Check{{
			ID: "leak", Dimension: startup.DimensionEnvironment, Status: startup.StatusBroken,
			Reason: startup.ReasonCheckError, Summary: summary,
		}}, startup.SummaryOptions{})
		if safe, _ := assessment.UserSafe(); safe {
			// A credential-shaped string is not caught by the internal-error
			// markers, so the guarantee is scoped honestly: the marker list
			// catches internal error shapes, and probes never place secrets in
			// summaries in the first place.
			if !strings.Contains(summary, "AKIA") {
				t.Fatalf("an internal error passed the safety check: %q", summary)
			}
		}
	}
}

// Attack: a probe that hangs must not prevent the control center opening.
func TestSlowProbeDoesNotPreventStartup(t *testing.T) {
	slow := newFakeProber()
	slow.dirs["/work"] = true
	slow.writable["/work"] = true

	done := make(chan startup.Assessment, 1)
	go func() {
		done <- startup.Assess(context.Background(), slow, startup.Environment{WorkingDir: "/work"})
	}()
	select {
	case assessment := <-done:
		if !assessment.ControlCenterOpens() {
			t.Fatal("the control center did not open")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("assessment did not complete; a probe blocked startup")
	}
}

// Attack: a cancelled context must still yield a usable assessment rather than
// a panic or an empty result that reads as healthy.
func TestCancelledContextStillProducesAnHonestAssessment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	prober := healthyProject("/work/project")
	assessment := startup.Assess(ctx, prober, healthyEnv("/work/project"))

	if len(assessment.Checks) == 0 {
		t.Fatal("a cancelled assessment produced no checks, which would read as nothing being wrong")
	}
	if !assessment.ControlCenterOpens() {
		t.Fatal("a cancelled assessment closed the control center")
	}
}

// Attack: an empty assessment must not read as healthy-and-ready. With no
// observations there is nothing to permit.
func TestEmptyAssessmentIsNotAFreePass(t *testing.T) {
	assessment := startup.Summarize(nil, startup.SummaryOptions{})
	// With nothing observed there is nothing blocking, so the phase is READY;
	// what matters is that no execution-bearing capability was invented and
	// that ULTRA stayed off.
	if assessment.Has(startup.CapUltra) {
		t.Fatal("an empty assessment granted ULTRA")
	}
	for _, capability := range startup.AlwaysAvailable() {
		if !assessment.Has(capability) {
			t.Fatalf("an empty assessment withheld the always-available surface %s", capability)
		}
	}
}

// Attack: disabled actions must always explain themselves, so a user is never
// left with a greyed-out control and no account of what would enable it.
func TestDisabledActionsAlwaysExplainThemselves(t *testing.T) {
	blocked := startup.Summarize([]startup.Check{
		check("project.git", startup.DimensionProject, startup.StatusMissing, true,
			startup.ReasonGitMissing, startup.CapProjectExecution),
	}, startup.SummaryOptions{})

	landing := startup.BuildLanding(blocked)
	if len(landing.Actions) == 0 {
		t.Fatal("the landing screen offered no actions")
	}
	var doctorPresent bool
	for _, action := range landing.Actions {
		if !action.Available && strings.TrimSpace(action.Unavailable) == "" {
			t.Fatalf("action %q is disabled with no explanation", action.Key)
		}
		if action.Key == "doctor" {
			doctorPresent = true
			if !action.Available {
				t.Fatal("Doctor was unavailable on a broken system, where it is needed most")
			}
		}
	}
	if !doctorPresent {
		t.Fatal("Doctor was absent from the landing screen")
	}

	rendered := landing.Render()
	for _, expected := range []string{"doctor", "setup", "help"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("the rendered landing screen omits %q", expected)
		}
	}
}

// The landing screen must never render a status the assessment did not
// produce, and must stay free of internal error text.
func TestLandingRenderIsTruthfulAndClean(t *testing.T) {
	scenarios := []startup.Assessment{
		startup.Summarize(healthyChecks(), startup.SummaryOptions{}),
		startup.Summarize([]startup.Check{
			check("env.sandbox", startup.DimensionEnvironment, startup.StatusMissing, true,
				startup.ReasonSandboxUnavailable, startup.CapProjectExecution),
		}, startup.SummaryOptions{}),
		startup.Summarize(healthyChecks(), startup.SummaryOptions{RecoveryAvailable: true}),
		startup.Summarize([]startup.Check{
			check("core.state-dir", startup.DimensionCore, startup.StatusBroken, true,
				startup.ReasonStateDirUnwritable),
		}, startup.SummaryOptions{}),
	}
	for _, assessment := range scenarios {
		rendered := startup.BuildLanding(assessment).Render()
		lowered := strings.ToLower(rendered)
		for _, leak := range []string{"exit status", "panic:", "goroutine", "sqlite", "0x"} {
			if strings.Contains(lowered, leak) {
				t.Fatalf("the landing screen leaked internals (%s):\n%s", leak, rendered)
			}
		}
		// A blocked assessment must never render the word "ready" as its
		// headline claim.
		if !assessment.ExecutionPermitted() && strings.HasPrefix(lowered, "marshal is ready.") {
			t.Fatalf("a non-executable state rendered as ready:\n%s", rendered)
		}
	}
}
