package tui

import (
	"context"
	"strings"
	"testing"
)

func TestStoreDiagnosticsCommands(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, ctx := newControlWorkspace(t)
	for _, tc := range []struct{ command, want string }{
		{"/store", "quick_check passed"},
		{"/store check quick", "quick_check passed"},
		{"/store check full", "integrity_check passed"},
		{"/store counts", "Inventory counts (not proof of health)"},
		{"/store check", "Usage:"}, {"/store check typo", "Usage:"},
		{"/store counts sqlite_master", "Usage:"}, {"/store check full extra", "Usage:"},
	} {
		out, err := ws.ExecuteCommand(ctx, tc.command)
		if err != nil || !strings.Contains(out, tc.want) {
			t.Errorf("%s: %q %v", tc.command, out, err)
		}
	}
	help, err := ws.ExecuteCommand(ctx, "/help all")
	if err != nil || !strings.Contains(help, "check full") || !strings.Contains(help, "full can take long") {
		t.Fatalf("help: %s %v", help, err)
	}
	for key, want := range map[string]string{"/store": "check counts", "/store check": "quick full"} {
		if strings.Join(ws.completer.ctx.Subcommands[key], " ") != want {
			t.Errorf("completion %s: %v", key, ws.completer.ctx.Subcommands[key])
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ws.ExecuteCommand(ctx, "/store"); err == nil {
		t.Fatal("cancelled diagnostics succeeded")
	}
}
