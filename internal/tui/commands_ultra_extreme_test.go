//go:build !governed

package tui

import (
	"context"
	"strings"
	"testing"
)

func TestUltraCannotBeSwitchedInMarshalBuild(t *testing.T) {
	ws := NewWorkspace(nil, "project", "session")
	t.Cleanup(ws.Close)
	ws.setUltraExecution(true)
	if got := ws.completer.ctx.Subcommands["/ultra"]; len(got) != 0 {
		t.Fatalf("shipped /ultra completion offers subcommands: %v", got)
	}
	help, err := ws.ExecuteCommand(context.Background(), "/help all")
	if err != nil || strings.Contains(help, "/ultra") {
		t.Fatalf("shipped help mentions /ultra: err=%v\n%s", err, help)
	}
	mode, err := ws.ExecuteCommand(context.Background(), "/mode")
	if err != nil || strings.Contains(mode, "/ultra start") || strings.Contains(mode, "/ultra stop") {
		t.Fatalf("shipped mode text suggests /ultra switching: err=%v\n%s", err, mode)
	}
	before := ws.ULTRAExecutionEnabled()
	for _, line := range []string{"/ultra", "/ultra start", "/ultra stop", "/ultra stop confirm", "/ultra request"} {
		out, err := ws.ExecuteCommand(context.Background(), line)
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if out != ultraFixedAnswer {
			t.Errorf("%s = %q, want %q", line, out, ultraFixedAnswer)
		}
		if got := ws.ULTRAExecutionEnabled(); got != before {
			t.Errorf("%s changed ULTRA execution: before=%t after=%t", line, before, got)
		}
	}
}
