package cli

import (
	"context"
	"fmt"

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
	if len(args) > 0 && args[0] != "" {
		sessionID = args[0]
	}

	ws := tui.NewWorkspace(rt.Store(), rt.ProjectID(), sessionID)
	return ws.Run(ctx, c.stdin, c.stdout)
}
