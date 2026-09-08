package cli

import (
	"context"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
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
