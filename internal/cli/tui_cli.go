package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/tui"
)

// tui validates the project before launching the control center.
func (c *command) tui(ctx context.Context, args []string) error {
	return c.controlCenter(ctx, args)
}

// runWorkspace launches the full interactive workspace for a ready project.
func (c *command) runWorkspace(ctx context.Context, runtime *app.Runtime, args []string) error {
	options := parseWorkspaceArgs(args)
	workspace := tui.NewWorkspace(runtime.Store(), runtime.ProjectID(), options.sessionID)
	workspace.SetTheme(options.theme, options.animation)
	if options.nativeCodex != nil {
		workspace.StartWithNativeCodex(options.nativeCodex)
	}
	if options.nativeClaude != nil {
		workspace.StartWithNativeClaude(options.nativeClaude)
	}
	if options.nativeOpenCode != nil {
		workspace.StartWithNativeOpenCode(options.nativeOpenCode)
	}
	if options.nativeAntigravity != nil {
		workspace.StartWithNativeAntigravity(options.nativeAntigravity)
	}

	// Control submits every mutation through this runtime. Without it the
	// Control screens render but every action refuses, which is the truthful
	// behaviour for a workspace that cannot reach a canonical authority.
	//
	// The opened runtime has already admitted this project and resolved its
	// canonical root. Re-running Git discovery here can fail transiently after
	// the runtime opened, leaving the workspace without any control authority.
	// The identity still comes from the binding on disk, never from a guess.
	if binding, found := projectid.LoadBinding(
		filepath.Join(runtime.ProjectRoot(), projectid.StateDirName)); found &&
		binding.ID == runtime.AdmittedProjectIdentity() {
		workspace.AttachRuntime(runtime, binding.ID)
	} else {
		// Without a binding the execution service cannot be addressed, so
		// Control refuses rather than acting on a guessed identity.
		workspace.AttachRuntime(nil, "")
	}

	// Bring up the Community Cloud for this session, if one is configured.
	//
	// Every failure here is a mode, not an error: an unconfigured or unreachable
	// Cloud leaves a nil gate, which answers "not entitled" to everything, so
	// the workspace opens in Standard exactly as it would offline. That is why
	// the error is not returned — losing ULTRA must never cost somebody their
	// session.
	root, err := c.projectRoot(ctx)
	if err == nil {
		authorization := cloudAuthorize(ctx, root)
		workspace.AttachULTRASession(authorization)
		observeULTRAExecution(ctx, workspace.ULTRAExecutionEnabled)
		// Compose the persisted ULTRA-entitlement request service. It is rooted
		// alongside the machine installation identity (os.UserConfigDir()/marshal),
		// never the production config or the source tree. The workspace and the
		// runtime share this single service, so Status, Models and the request
		// action all observe the same durable attempt.
		if runtime != nil && authorization.Client != nil {
			stateDir := filepath.Join(root, projectid.StateDirName)
			if dir, err := cloud.MachineStateDir(stateDir); err == nil {
				stateDir = dir
			}
			if svc, svcErr := app.NewULTRARequestService(runtime, authorization.Client, authorization.State,
				options.sessionID, authorization.SessionID, cloud.NewRequestStore(stateDir)); svcErr == nil {
				runtime.SetWorkspaceEntitlementRequests(svc)
			}
		}
		authorization.Start(ctx)
		defer authorization.Stop()
	}

	tmuxRoot := c.canonicalRoot(ctx)
	workspace.InitTmux(tmuxRoot)
	// This launcher execs `marshal tui SESSION-ID` when the operator opens
	// another session (relaunchSession), so the workspace may offer it.
	workspace.EnableSessionRelaunch()

	return workspace.Run(ctx, c.stdin, c.stdout)
}

type workspaceOptions struct {
	sessionID      string
	theme          tui.ThemeMode
	animation      bool
	nativeCodex    []string
	nativeClaude   []string
	nativeOpenCode []string
	// nativeAntigravity carries agy's arguments when MARSHAL was launched as
	// `marshal agy`.
	nativeAntigravity []string
}

func parseWorkspaceArgs(args []string) workspaceOptions {
	options := workspaceOptions{
		sessionID: "default-session",
		theme:     tui.ThemeDefault,
		animation: true,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--claude":
			options.nativeClaude = append([]string{}, args[i+1:]...)
			return options
		case arg == "--codex":
			options.nativeCodex = append([]string{}, args[i+1:]...)
			return options
		case arg == "--opencode":
			options.nativeOpenCode = append([]string{}, args[i+1:]...)
			return options
		case arg == "--antigravity":
			options.nativeAntigravity = append([]string{}, args[i+1:]...)
			return options
		case arg == "--no-animation":
			options.animation = false
		case arg == "--no-color":
			options.theme = tui.ThemeNoColor
		case strings.HasPrefix(arg, "--theme="):
			options.theme = tui.ThemeMode(strings.TrimPrefix(arg, "--theme="))
		case arg == "--theme" && i+1 < len(args):
			options.theme = tui.ThemeMode(args[i+1])
			i++
		case !strings.HasPrefix(arg, "-") && options.sessionID == "default-session":
			options.sessionID = arg
		}
	}
	return options
}

// relaunchExec replaces this process with argv; tests replace it.
var relaunchExec = syscall.Exec

// relaunchSession finishes a workspace that returned tui.SessionRelaunch:
// the workspace and its runtime have closed, so this process replaces itself
// with `marshal tui SESSION-ID` on the same terminal. Inside tmux the pane
// and its window stay as they were (the Marshal chat left the dock on exit
// and the new workspace docks it again); the new workspace binds every
// authority to the chosen session from its start.
// Any other result passes through.
func (c *command) relaunchSession(err error, tuiArgs []string) error {
	var r *tui.SessionRelaunch
	if !errors.As(err, &r) {
		return err
	}
	if c.stopTelemetry != nil {
		c.stopTelemetry()
	}
	exe, xerr := os.Executable()
	if xerr == nil {
		xerr = relaunchExec(exe, relaunchArgs(exe, tuiArgs, r.SessionID, tmux.IsInsideTmux()), os.Environ())
	}
	return fmt.Errorf("open session %s: %w; run `marshal tui %s`", r.SessionID, xerr, r.SessionID)
}

// relaunchArgs is the command line of the relaunched workspace: the display
// options of this one, none of its native-launch arguments (they belonged to
// the session left), and the session to open. Inside tmux the launcher has
// already reported this start, so the child flag keeps it from reporting a
// second one.
func relaunchArgs(exe string, tuiArgs []string, session string, insideTmux bool) []string {
	argv := []string{exe, "tui"}
	if insideTmux {
		argv = append(argv, telemetryChildFlag)
	}
	for i := 0; i < len(tuiArgs); i++ {
		a := tuiArgs[i]
		switch {
		case a == "--no-animation", a == "--no-color", strings.HasPrefix(a, "--theme="):
			argv = append(argv, a)
		case a == "--theme" && i+1 < len(tuiArgs):
			argv = append(argv, a, tuiArgs[i+1])
			i++
		case a == "--claude", a == "--codex", a == "--opencode", a == "--antigravity":
			i = len(tuiArgs)
		}
	}
	return append(argv, session)
}
