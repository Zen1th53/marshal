package cli

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/tui"
)

// tui enters the control center. It no longer opens the execution runtime
// first: startup is assessed, and a project that cannot be opened produces a
// landing screen explaining why rather than a raw error on stderr.
func (c *command) tui(ctx context.Context, args []string) error {
	return c.controlCenter(ctx, args)
}

// runWorkspace launches the full interactive workspace for a ready project.
func (c *command) runWorkspace(ctx context.Context, runtime *app.Runtime, args []string) error {
	options := parseWorkspaceArgs(args)
	workspace := tui.NewWorkspace(runtime.Store(), runtime.ProjectID(), options.sessionID)
	workspace.SetTheme(options.theme, options.animation)

	// Bring up the Community Cloud for this session, if one is configured.
	//
	// Every failure here is a mode, not an error: an unconfigured or unreachable
	// Cloud leaves a nil gate, which answers "not entitled" to everything, so
	// the workspace opens in Standard exactly as it would offline. That is why
	// the error is not returned — losing ULTRA must never cost somebody their
	// session.
	root, err := c.projectRoot(ctx)
	if err == nil {
		authorization := cloud.Authorize(ctx, cloud.LoadConfig(),
			filepath.Join(root, projectid.StateDirName), constitution.Current.String())
		workspace.AttachULTRA(authorization.Gate, authorization.ExecutionEnabled)
		// The requester is attached even when the gate is nil, because a
		// session that is *not* entitled is exactly the one with something to
		// ask for.
		workspace.AttachULTRARequester(authorization.Client, authorization.State,
			authorization.SessionID)
		authorization.Start(ctx)
		defer authorization.Stop()
	}

	return workspace.Run(ctx, c.stdin, c.stdout)
}

type workspaceOptions struct {
	sessionID string
	theme     tui.ThemeMode
	animation bool
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
