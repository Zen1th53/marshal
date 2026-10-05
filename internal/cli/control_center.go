package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/startup"
	"github.com/Zen1th53/marshal/internal/tmux"
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
	if _, err := tmux.FindBinary(); err != nil {
		if err != tmux.ErrTmuxMissing {
			return err
		}
		hint := tmux.DetectInstallCommand()
		fmt.Fprintf(c.stderr, "tmux is required to run the MARSHAL TUI.\nInstall tmux with:\n  %s\n", hint)
		return fmt.Errorf("tmux is required to run the MARSHAL TUI; install with: %s", hint)
	}

	canonicalRoot := c.canonicalRoot(ctx)

	// Decision 1: The MARSHAL TUI ALWAYS runs inside tmux.
	// If MARSHAL is not started inside tmux, it creates (or re-attaches to) a dedicated
	// per-project tmux session and runs itself inside it. If it is already inside tmux,
	// it uses the current session (never nest tmux).
	if !directPTYTest && !tmux.IsInsideTmux() && (c.isInteractiveTerminal() || os.Getenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH") == "1") {
		return c.launchOrAttachTmux(ctx, canonicalRoot, args)
	}

	assessment := startup.Assess(ctx, startup.NewSystemProber(), c.startupEnvironment())

	// A genuine core failure is the only case where nothing can be shown. Even
	// then the user gets the reason and a remedy rather than a raw error.
	if !assessment.ControlCenterOpens() {
		landing := startup.BuildLanding(assessment)
		fmt.Fprint(c.stdout, landing.Render())
		return errCoreFailure
	}

	// Offer first-run Git setup before trying to open a workspace. Reuse setup's
	// separate confirmations and re-assessment, including project initialization.
	if check, found := assessment.Check("project.repository"); found &&
		(check.Reason == startup.ReasonNotAGitRepository || check.Reason == startup.ReasonRepositoryEmpty) &&
		!c.json && c.confirmer() != nil {
		assessment = c.offerSetupFixes(ctx, assessment)
	}

	// A ready project gets the full workspace. This is the only path that
	// opens the execution runtime, and it is reached only when the assessment
	// already established that the project is usable.
	if assessment.ExecutionPermitted() {
		runtime, err := app.Open(ctx, canonicalRoot)
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

func (c *command) launchOrAttachTmux(ctx context.Context, root string, args []string) error {
	sessionName := tmux.SessionName(root)
	exe, err := os.Executable()
	if err != nil {
		exe = "marshal"
	}
	cmd := []string{exe, "tui"}
	cmd = append(cmd, args...)

	if !tmux.HasSession(ctx, sessionName) {
		if err := tmux.NewSession(ctx, sessionName, root, "marshal", cmd); err != nil {
			return fmt.Errorf("start tmux session: %w", err)
		}
	} else {
		// Session already exists: verify Marshal window is alive, repair/respawn if exited
		target := sessionName + ":marshal"
		if dead, _ := tmux.IsPaneDead(ctx, target); dead {
			_ = tmux.RespawnWindow(ctx, target, cmd)
		} else if exists, _ := tmux.WindowExists(ctx, sessionName, "marshal"); !exists {
			_ = tmux.NewWindow(ctx, sessionName, "marshal", root, nil, cmd)
		}
	}
	return tmux.AttachSession(sessionName, c.stdin, c.stdout, c.stderr)
}

func (c *command) canonicalRoot(ctx context.Context) string {
	if root, err := c.projectRoot(ctx); err == nil && root != "" {
		if r, err := filepath.EvalSymlinks(root); err == nil {
			return r
		}
		return root
	}
	if r, err := filepath.EvalSymlinks(c.root); err == nil {
		return r
	}
	return c.root
}

func (c *command) isInteractiveTerminal() bool {
	fIn, okIn := c.stdin.(*os.File)
	fOut, okOut := c.stdout.(*os.File)
	if !okIn || !okOut || fIn == nil || fOut == nil {
		return false
	}
	termType := strings.TrimSpace(os.Getenv("TERM"))
	if termType == "" || termType == "dumb" {
		return false
	}
	return term.IsTerminal(int(fIn.Fd())) && term.IsTerminal(int(fOut.Fd()))
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
