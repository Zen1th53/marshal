//go:build !governed

package tui

import (
	"github.com/Zen1th53/marshal/internal/cloud"
	"strings"
	"testing"
)

func TestUltraCanBeSwitchedInMarshalBuild(t *testing.T) {
	ws := NewWorkspace(nil, "project", "session")
	defer ws.Close()
	ws.AttachULTRASession(cloud.Authorize(t.Context(), cloud.Config{}, t.TempDir(), "test"))
	if got := ws.completer.ctx.Subcommands["/ultra"]; len(got) != 3 {
		t.Fatalf("completion: %v", got)
	}
	help, err := ws.ExecuteCommand(t.Context(), "/help all")
	if err != nil || !strings.Contains(help, "/ultra") {
		t.Fatalf("help: %s %v", help, err)
	}
	for _, line := range []string{"/ultra stop", "/ultra stop confirm"} {
		if _, err := ws.ExecuteCommand(t.Context(), line); err != nil {
			t.Fatal(err)
		}
	}
	if ws.ULTRAExecutionEnabled() {
		t.Fatal("off was ignored")
	}
	if _, err := ws.ExecuteCommand(t.Context(), "/ultra start"); err != nil {
		t.Fatal(err)
	}
	if !ws.ULTRAExecutionEnabled() {
		t.Fatal("start was ignored")
	}
}
