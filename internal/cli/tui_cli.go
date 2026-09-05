package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/tui"
)

func (c *command) tui(ctx context.Context, args []string) error {
	rt, err := app.Open(ctx, c.root)
	if err != nil {
		return fmt.Errorf("open runtime for TUI: %w", err)
	}
	defer rt.Close()

	sessionID := "default-session"
	themeMode := tui.ThemeDefault
	animation := true

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--no-animation":
			animation = false
		case arg == "--no-color":
			themeMode = tui.ThemeNoColor
		case strings.HasPrefix(arg, "--theme="):
			themeMode = tui.ThemeMode(strings.TrimPrefix(arg, "--theme="))
		case arg == "--theme" && i+1 < len(args):
			themeMode = tui.ThemeMode(args[i+1])
			i++
		case !strings.HasPrefix(arg, "-") && sessionID == "default-session":
			sessionID = arg
		}
	}

	ws := tui.NewWorkspace(rt.Store(), rt.ProjectID(), sessionID)
	ws.SetTheme(themeMode, animation)
	return ws.Run(ctx, c.stdin, c.stdout)
}
