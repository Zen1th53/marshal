package cli

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/startup"
)

// controlCenter is the Process 01 entry point: what happens when a user runs
// `marshal` or `marshal tui`.
//
// The order here is the whole point. Startup is assessed *before* the
// execution runtime is opened, so a runtime that cannot open becomes a
// described condition on a screen rather than a terminated process with a
// subprocess error on stderr. The full workspace is opened only when the
// assessment says a project is genuinely ready; otherwise the landing view is
// shown, which is still the control center — just with fewer actions enabled.
//
// The execution runtime keeps its existing strictness. Nothing here makes
// app.Open more permissive; it simply stops being the gate on whether a user
// can see anything at all.
func (c *command) controlCenter(ctx context.Context, args []string) error {
	assessment := startup.Assess(ctx, startup.NewSystemProber(), c.startupEnvironment())

	// A genuine core failure is the only case where nothing can be shown. Even
	// then the user gets the reason and a remedy rather than a raw error.
	if !assessment.ControlCenterOpens() {
		landing := startup.BuildLanding(assessment)
		fmt.Fprint(c.stdout, landing.Render())
		return errCoreFailure
	}

	// A ready project gets the full workspace. This is the only path that
	// opens the execution runtime, and it is reached only when the assessment
	// already established that the project is usable.
	if assessment.ExecutionPermitted() {
		runtime, err := app.Open(ctx, c.root)
		if err == nil {
			defer runtime.Close()
			return c.runWorkspace(ctx, runtime, args)
		}
		// The assessment said the project was ready and the runtime disagreed.
		// That gap is itself worth reporting: it is re-assessed so the user
		// sees a described condition rather than the raw open error.
		assessment = startup.Assess(ctx, startup.NewSystemProber(), c.startupEnvironment())
	}

	fmt.Fprint(c.stdout, startup.BuildLanding(assessment).Render())
	return nil
}

// startupEnvironment gathers the facts assessment needs that it cannot observe
// for itself.
//
// Sandbox and network enforcement are reported from what the runtime actually
// determines, not guessed from the presence of a binary. Both currently
// resolve conservatively: an unproven capability is reported absent, so
// execution blocks rather than proceeding unprotected.
func (c *command) startupEnvironment() startup.Environment {
	return startup.Environment{
		WorkingDir:      c.root,
		SandboxEnforced: app.SandboxEnforcementAvailable(),
		NetworkEnforced: app.EgressEnforcementAvailable(),
		// ULTRA is never inferred at startup. It stays off until an
		// entitlement check verifies a signed, unexpired grant.
		UltraEntitled: false,
	}
}

// errCoreFailure signals that MARSHAL itself cannot run here. It is distinct
// from an ordinary command failure so the exit code can reflect that the
// control center never opened.
var errCoreFailure = fmt.Errorf("marshal cannot start in this directory")
