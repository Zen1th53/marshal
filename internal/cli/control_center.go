package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/startup"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// controlCenter validates the project before creating a tmux session or opening
// the execution runtime. Setup and doctor remain available for diagnostics.
func (c *command) controlCenter(ctx context.Context, args []string) error {
	if _, err := tmux.FindBinary(); err != nil {
		if err != tmux.ErrTmuxMissing {
			return err
		}
		hint := tmux.DetectInstallCommand()
		fmt.Fprintf(c.stderr, "tmux is required to run the MARSHAL TUI.\nInstall tmux with:\n  %s\n", hint)
		return fmt.Errorf("tmux is required to run the MARSHAL TUI; install with: %s", hint)
	}

	// A dedicated folder without a repository of its own gets one, and then
	// Marshal is set up in it, as `marshal init` would: the person asked to
	// work here, and a repository Marshal just started has nothing else in it
	// to wait for.
	started, err := c.prepareProjectRepository(ctx)
	if err != nil {
		return err
	}
	if started {
		if _, _, err := initializeProject(ctx, c.root); err != nil {
			return err
		}
	}
	layout, err := app.ValidateProject(ctx, c.root)
	if err != nil {
		return err
	}
	canonicalRoot := layout.Root

	assessment := startup.Assess(ctx, startup.NewSystemProber(), c.startupEnvironment())

	// A genuine core failure is the only case where nothing can be shown. Even
	// then the user gets the reason and a remedy rather than a raw error.
	if !assessment.ControlCenterOpens() {
		landing := startup.BuildLanding(assessment)
		fmt.Fprint(c.stderr, landing.Render())
		return errCoreFailure
	}

	if !assessment.ExecutionPermitted() {
		fmt.Fprint(c.stderr, startup.BuildLanding(assessment).Render())
		return fmt.Errorf("MARSHAL project %q cannot start; run `marshal doctor` for details", canonicalRoot)
	}

	// Decision 1: The MARSHAL TUI ALWAYS runs inside tmux.
	// If MARSHAL is not started inside tmux, it creates (or re-attaches to) a dedicated
	// per-project tmux session and runs itself inside it. If it is already inside tmux,
	// it uses the current session (never nest tmux).
	if !directPTYTest && !tmux.IsInsideTmux() && (c.isInteractiveTerminal() || os.Getenv("MARSHAL_TEST_FORCE_TMUX_LAUNCH") == "1") {
		// A missing coding tool does not block the control center from opening,
		// but nothing can run until one is installed, so say it up front rather
		// than at the first failed run. Printed on this outer launch only, so it
		// does not land on the terminal the in-tmux TUI child then takes over.
		if h, ok := assessment.Check("env.harness"); ok && h.Status == startup.StatusMissing {
			fmt.Fprintf(c.stderr, "Warning: %s\n  %s\n  Run `marshal doctor` for details.\n", h.Impact, h.Remedy)
		}
		return c.launchOrAttachTmux(ctx, canonicalRoot, args)
	}

	runtime, err := app.Open(ctx, canonicalRoot)
	if err != nil {
		return fmt.Errorf("open MARSHAL project %q: %w; run `marshal doctor` for details", canonicalRoot, err)
	}
	defer runtime.Close()
	werr := c.runWorkspace(ctx, runtime, args)
	// Close before a session relaunch execs over this process.
	runtime.Close()
	return werr
}

func (c *command) launchOrAttachTmux(ctx context.Context, root string, args []string) error {
	if err := statepath.Validate(root); err != nil {
		return err
	}
	sessionName := tmux.SessionName(root)
	exe, err := os.Executable()
	if err != nil {
		exe = "marshal"
	}
	cmd := []string{exe, "tui", telemetryChildFlag}
	cmd = append(cmd, args...)

	if !tmux.HasSession(ctx, sessionName) {
		cmd, err = prepareTUIChild(root, cmd)
		if err != nil {
			return err
		}
		if err := tmux.NewSession(ctx, sessionName, root, statepath.Name, cmd); err != nil {
			return tuiChildResult(root, fmt.Errorf("start tmux session: %w", err))
		}
	} else {
		// Session already exists: verify Marshal window is alive, repair/respawn if exited.
		// The window is named exactly ("=name"): a bare name also matches
		// a prefix, so after /quit it found a leftover provider window
		// (marshal-codex-…), respawned that and then could not select the
		// control centre at all.
		if exists, _ := tmux.WindowExists(ctx, sessionName, statepath.Name); !exists {
			cmd, err = prepareTUIChild(root, cmd)
			if err == nil {
				err = tmux.NewWindow(ctx, sessionName, statepath.Name, root, nil, cmd)
			}
			if err != nil {
				return tuiChildResult(root, err)
			}
		} else if dead, _ := tmux.IsPaneDead(ctx, controlCentreTarget(sessionName)); dead {
			cmd, err = prepareTUIChild(root, cmd)
			if err == nil {
				err = tmux.RespawnWindowIn(ctx, controlCentreTarget(sessionName), root, cmd)
			}
			if err != nil {
				return tuiChildResult(root, err)
			}
		}
	}
	// Re-attachment may find an agent selected from the previous visit.
	// Always enter through the control centre; chat and workers keep running.
	if err := tmux.SelectWindow(ctx, controlCentreTarget(sessionName)); err != nil {
		return tuiChildResult(root, fmt.Errorf("select control centre: %w", err))
	}
	return tuiChildResult(root, tmux.AttachSessionContext(ctx, sessionName, c.stdin, c.stdout, c.stderr))
}

// controlCentreTarget is the control centre window of sessionName, matched
// by its exact name.
func controlCentreTarget(sessionName string) string {
	return sessionName + ":=" + statepath.Name
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
// Sandbox and network enforcement come from what the runtime actually
// determines. The Marshal build runs workers on the host and never uses
// bubblewrap, so it does not probe for it: probing would start bwrap on
// every launch for an answer startup ignores in this build.
func (c *command) startupEnvironment() startup.Environment {
	env := startup.Environment{
		WorkingDir: c.root,
		// The build-specific authorization supplies readiness evidence;
		// Community keeps its conservative startup default.
		UltraEntitled: startupUltraEntitled(),
	}
	if !sandbox.HostExecutionPreferred() {
		env.SandboxEnforced = app.SandboxEnforcementAvailable()
		env.NetworkEnforced = app.EgressEnforcementAvailable()
	}
	return env
}

// errCoreFailure signals that MARSHAL itself cannot run here. It is distinct
// from an ordinary command failure so the exit code can reflect that the
// control center never opened.
var errCoreFailure = fmt.Errorf("marshal cannot start in this directory")
