package tui

import (
	"context"
	"strings"
	"testing"
)

// Plain text must never start an agent. Launching one spends the operator's
// tokens and can touch the worktree, so a typo or a stray paste must not be
// read as consent to run anything.
func TestPlainTextStartsNothing(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	for _, line := range []string{
		"hello",
		"fix the watcher please",
		"rm -rf /",
	} {
		out, err := ws.ExecuteCommand(ctx, line)
		if err != nil {
			t.Fatalf("%q returned an error: %v", line, err)
		}
		if !strings.Contains(out, "Nothing was run") {
			t.Errorf("%q did not report that it ran nothing:\n%s", line, out)
		}
		if ws.nativeProvider != "" {
			t.Fatalf("%q opened a native %s session", line, ws.nativeProvider)
		}
	}

	// Blank input is answered earlier and separately; it must also start nothing.
	if _, err := ws.ExecuteCommand(ctx, "    "); err != nil {
		t.Fatalf("blank input errored: %v", err)
	}
	if ws.nativeProvider != "" {
		t.Fatalf("blank input opened a native %s session", ws.nativeProvider)
	}
}

// Having used an agent does not make the next bare line a prompt for it. The
// last-opened provider is recorded, but it is never routed to.
func TestPlainTextStartsNothingAfterAnAgentWasUsed(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	ws.nativeProvider = "codex"

	out, err := ws.ExecuteCommand(ctx, "carry on")
	if err != nil {
		t.Fatalf("plain text errored: %v", err)
	}
	if !strings.Contains(out, "Nothing was run") {
		t.Errorf("a bare line was routed to the last opened agent:\n%s", out)
	}
}

// The commonest way to land here is a command typed without its slash, so that
// case is answered with the command rather than with a list of alternatives.
func TestPlainTextSuggestsTheMissingSlash(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	out, err := ws.ExecuteCommand(ctx, "status")
	if err != nil {
		t.Fatalf("plain text errored: %v", err)
	}
	if !strings.Contains(out, "Did you mean /status?") {
		t.Errorf("a slashless command was not recognised:\n%s", out)
	}
}

// A word that is not a command gets the explicit routes instead of a guess.
func TestPlainTextNamesTheExplicitRoutes(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	out, err := ws.ExecuteCommand(ctx, "refactor the parser")
	if err != nil {
		t.Fatalf("plain text errored: %v", err)
	}
	for _, want := range []string{"/codex <prompt>", "/claude <prompt>", "/opencode <prompt>", "F7 / F8 / F9"} {
		if !strings.Contains(out, want) {
			t.Errorf("route %q missing from the answer:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Did you mean") {
		t.Errorf("a non-command was guessed at:\n%s", out)
	}
}

func TestKnownCommandRecognisesOnlyRealCommands(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-1")
	if !ws.knownCommand("/status") {
		t.Error("/status was not recognised")
	}
	if ws.knownCommand("/stat") {
		t.Error("a prefix was treated as a whole command")
	}
	if ws.knownCommand("/definitely-not-a-command") {
		t.Error("an unknown token was treated as a command")
	}
	if (&Workspace{}).knownCommand("/status") {
		t.Error("a workspace with no completer claimed to know a command")
	}
	_ = context.Background()
}
