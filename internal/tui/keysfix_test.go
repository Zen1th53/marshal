//go:build linux

package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKeysfixOpenCodeEmptyRun(t *testing.T) {
	_, w, ctx := newControlWorkspace(t)
	for _, cmd := range []string{"/opencode run", `/opencode run ""`, "/opencode cli run"} {
		out, err := w.ExecuteCommand(ctx, cmd)
		if err != nil || !strings.Contains(out, "Usage: /opencode run <text>") {
			t.Fatalf("%s: %s %v", cmd, out, err)
		}
	}
}

func TestKeysfixHelpPreservesStart(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	out := "FIRST\n" + strings.Repeat("middle\n", 250) + "LAST"
	w.recordCommandResult("/help all", out, nil)
	lines := outputSection(w.GetUIState(), w.theme, 80)
	if !strings.Contains(strings.Join(lines, "\n"), "FIRST") || !strings.Contains(strings.Join(lines, "\n"), "LAST") || w.scrollOffset == 0 {
		t.Fatal("full help must preserve every line and open at start")
	}
}

func TestKeysfixOpenCodeCatalogRoutable(t *testing.T) {
	for _, op := range providerHelpOperations("opencode") {
		if strings.HasPrefix(op, "plugin") {
			t.Fatalf("unrouted operation advertised: %s", op)
		}
	}
}

func TestKeysfixReviewDefault(t *testing.T) {
	t.Chdir(testgit.New(t).Path())
	_, w, ctx := newControlWorkspace(t)
	for _, cmd := range []string{"/review", "/codex review"} {
		out, err := w.ExecuteCommand(ctx, cmd)
		if err != nil || !strings.Contains(out, "No uncommitted changes to review") {
			t.Fatalf("%s: %s %v", cmd, out, err)
		}
	}
}

func TestKeysfixChatWithoutStoredRun(t *testing.T) {
	_, w, ctx := acceptanceWorkspace(t)
	svc, _, _, err := w.marshalService(ctx, "RUN-chat-only")
	if err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	m.service, m.runID = svc, "RUN-chat-only"
	for _, command := range []string{"/marshal approve", "/marshal close", "/marshal resume", "/marshal amend reason", "/marshal return task reason"} {
		out, err := w.ExecuteCommand(context.Background(), command)
		if err != nil {
			out += err.Error()
		}
		if !strings.Contains(out, "No Marshal run yet; start one with /marshal chat") || strings.Contains(out, "marshal_runs") {
			t.Fatalf("%s: %s", command, out)
		}
	}
}

func TestKeysfixRunControlGrammarAndApprovalHelp(t *testing.T) {
	_, w, ctx := acceptanceWorkspace(t)
	for _, item := range []struct{ cmd, want string }{{"/pause", "paused"}, {"/resume", "resumed"}, {"/cancel", "cancelled"}} {
		out, err := w.ExecuteCommand(ctx, item.cmd)
		if err != nil || !strings.Contains(out, "can be "+item.want) {
			t.Fatalf("%s: %s %v", item.cmd, out, err)
		}
	}
	out, err := w.ExecuteCommand(ctx, "/help all")
	if err != nil || !strings.Contains(out, "/approval [inspect|diff]") || strings.Contains(out, "/approval                Show native Codex approval policy") {
		t.Fatalf("approval help: %s %v", out, err)
	}
}

func TestKeysfixActiveNativeOperationRefused(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			w := realTmuxWorkspace(t)
			_, err := w.runNativeAgentInTmux(t.Context(), nativeLaunchOperator, provider, provider, w.workDir, "/bin/sh", []string{"-c", "read answer"}, nil, nil, nil, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"review", "--help"}, {"mcp", "list", "--help"}, {"--help"}} {
				out, err := w.runNativeAgentInTmux(t.Context(), nativeLaunchOperator, provider, provider, w.workDir, "/bin/sh", args, nil, nil, nil, nil, nil, nil, nil)
				if err == nil || !strings.Contains(err.Error(), "Nothing was run") || !strings.Contains(err.Error(), "retry") || out != "" {
					t.Fatalf("discarded operation %v: %s %v", args, out, err)
				}
			}
			out, err := w.runNativeAgentInTmux(t.Context(), nativeLaunchOperator, provider, provider, w.workDir, "/bin/sh", nil, nil, nil, nil, nil, nil, nil, nil)
			if err != nil || !strings.Contains(out, "Switched to active") {
				t.Fatalf("navigation: %s %v", out, err)
			}
		})
	}
}

func TestKeysfixDirtyReviewTargetsUncommitted(t *testing.T) {
	w := realTmuxWorkspace(t)
	_, attached, _ := acceptanceWorkspace(t)
	w.runtime = attached.runtime
	w.projectID, w.sessionID = attached.projectID, attached.sessionID
	w.terminal = &Terminal{isTerm: true, out: io.Discard, inFd: -1, outFd: -1}
	bin := t.TempDir()
	script := filepath.Join(bin, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'codex 0.160.1'; exit; fi\nif [ \"$1\" = \"--help\" ]; then echo review; exit; fi\nread answer\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(w.workDir, "change.txt"), []byte("change"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := w.ExecuteCommand(t.Context(), "/review")
	if err != nil {
		t.Fatalf("%s %v", out, err)
	}
	w.tmuxMu.Lock()
	a := copyAgentLocked(w.tmuxActiveWins["codex"])
	w.tmuxMu.Unlock()
	if a == nil || !strings.Contains(strings.Join(a.args, " "), "review --uncommitted") {
		t.Fatalf("review did not target uncommitted changes: %+v (%s)", a, out)
	}
}

func TestKeysfixNativeExitHasNoWorkerAlert(t *testing.T) {
	w := realTmuxWorkspace(t)
	_, err := w.runNativeAgentInTmux(t.Context(), nativeLaunchOperator, "codex", "Codex", w.workDir, "/bin/sh", []string{"-c", "exit 1"}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.tmuxMu.Lock()
		active, alerts := w.tmuxActiveWins["codex"] != nil, len(w.tmuxAlerts)
		w.tmuxMu.Unlock()
		if !active {
			if alerts != 0 {
				t.Fatal("ordinary native exit created a worker alert without a run identity")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("native exit did not settle")
}

func TestKeysfixPermissionStatusNextStep(t *testing.T) {
	_, w, ctx := newControlWorkspace(t)
	out, err := w.ExecuteCommand(ctx, "/permission status")
	if err != nil || !strings.Contains(out, "not available") || !strings.Contains(out, "/egress status") {
		t.Fatalf("%s %v", out, err)
	}
}

func TestKeysfixLocalProviderArgumentsNotDropped(t *testing.T) {
	_, w, ctx := newControlWorkspace(t)
	for _, cmd := range []string{"/codex status --help", "/claude help --help", "/codex models --help", "/opencode status --help", "/agy status --help"} {
		out, err := w.ExecuteCommand(ctx, cmd)
		if err != nil || !strings.Contains(out, "Nothing was run") || !strings.Contains(out, "cli") {
			t.Fatalf("%s: %s %v", cmd, out, err)
		}
	}
}

func TestKeysfixSafetyDestructiveConfirmationAndHelp(t *testing.T) {
	sweepWorkEnvironment(t)
	_, w, ctx := newControlWorkspace(t)
	installDialectDoubles(t)
	w.terminal = &Terminal{isTerm: true}

	// 1. /agy plugin uninstall --help must refuse help for subcommands that do not support it, never uninstalling
	out, err := w.ExecuteCommand(ctx, "/agy plugin uninstall --help")
	if err != nil || !strings.Contains(out, "does not support --help") || !strings.Contains(out, "agy plugin help") {
		t.Fatalf("expected clear refusal for /agy plugin uninstall --help, got: %s %v", out, err)
	}

	// 2. Destructive subcommands across providers require explicit operator confirmation
	cases := []struct {
		cmd string
		sub string
	}{
		{"/agy plugin uninstall my-plugin", "plugin uninstall"},
		{"/agy plugin disable my-plugin", "plugin disable"},
		{"/codex mcp remove srv", "mcp remove"},
		{"/claude plugin uninstall my-plugin", "plugin uninstall"},
		{"/opencode mcp logout srv", "mcp logout"},
	}
	for _, tc := range cases {
		out, err := w.ExecuteCommand(ctx, tc.cmd)
		if err != nil || !strings.Contains(out, "requires explicit operator confirmation") || !strings.Contains(out, "--confirm") {
			t.Fatalf("%s: expected confirmation prompt, got: %s %v", tc.cmd, out, err)
		}
	}

	// 3. extractConfirmation strips --confirm or confirm
	clean, ok := extractConfirmation([]string{"plugin", "uninstall", "test-plug", "--confirm"})
	if !ok || len(clean) != 3 || clean[0] != "plugin" || clean[1] != "uninstall" || clean[2] != "test-plug" {
		t.Fatalf("extractConfirmation failed: clean=%v, ok=%t", clean, ok)
	}
}

func TestKeysfixAgyCatalogGrammarAndRouting(t *testing.T) {
	_, w, ctx := newControlWorkspace(t)

	// 1. Advertised operations should not include unsupported ones
	agyOps := providerHelpOperations("agy")
	for _, unsupp := range []string{"mcp get", "mcp login", "mcp logout", "mcp auth", "mcp debug", "plugin marketplace"} {
		for _, op := range agyOps {
			if op == unsupp {
				t.Fatalf("unsupported operation %q advertised in agy help operations", unsupp)
			}
		}
	}

	// 2. Unsupported verbs return clear rejection
	for _, cmd := range []string{"/agy mcp login", "/agy mcp logout", "/agy mcp debug", "/agy plugin marketplace"} {
		out, err := w.ExecuteCommand(ctx, cmd)
		if err != nil || !strings.Contains(out, "UNSUPPORTED") {
			t.Fatalf("%s: expected UNSUPPORTED, got: %s %v", cmd, out, err)
		}
	}

	// 3. Arity checks for agy subcommands
	arityTests := []struct {
		cmd  string
		want string
	}{
		{"/agy plugin disable", "Usage: /agy plugin disable <name>"},
		{"/agy plugin enable", "Usage: /agy plugin enable <name>"},
		{"/agy plugin install", "Usage: /agy plugin install <target>"},
		{"/agy mcp remove", "Usage: /agy mcp remove <name>"},
	}
	for _, tc := range arityTests {
		out, err := w.ExecuteCommand(ctx, tc.cmd)
		if err != nil || !strings.Contains(out, tc.want) {
			t.Fatalf("%s: want %q, got: %s %v", tc.cmd, tc.want, out, err)
		}
	}
}

func TestKeysfixCodexHelpWhileSessionActive(t *testing.T) {
	w := realTmuxWorkspace(t)
	bin := t.TempDir()
	script := filepath.Join(bin, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'codex 0.160.1'; exit 0; fi\nif [ \"$2\" = \"--help\" ] || [ \"$1\" = \"--help\" ]; then echo 'NATIVE-CODEX-HELP-OUTPUT'; exit 0; fi\nread answer\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Start active native session in tmux
	_, err := w.runNativeAgentInTmux(t.Context(), nativeLaunchOperator, "codex", "Codex", w.workDir, "/bin/sh", []string{"-c", "read answer"}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Execute /codex review --help while active session is running
	out, err := w.ExecuteCommand(t.Context(), "/codex review --help")
	if err != nil || !strings.Contains(out, "NATIVE-CODEX-HELP-OUTPUT") {
		t.Fatalf("expected native help output, got: %s (err: %v)", out, err)
	}

	// Verify active session remains running
	w.tmuxMu.Lock()
	active := w.tmuxActiveWins["codex"] != nil
	w.tmuxMu.Unlock()
	if !active {
		t.Fatal("expected native Codex session to remain active after read-only help")
	}
}

func TestKeysfixShortcutsDAndQuestionMark(t *testing.T) {
	_, w, ctx := newControlWorkspace(t)

	// 1. When composer is empty:
	// Verify handleShortcut handles 'd' -> /diff and '?' -> /help
	if w.composer.Text() != "" {
		t.Fatal("expected empty composer initially")
	}

	handled := w.handleShortcut(ctx, KeyEvent{Type: KeyRune, Rune: 'd'})
	if !handled {
		t.Fatal("expected 'd' shortcut to be handled when composer is empty")
	}

	handled = w.handleShortcut(ctx, KeyEvent{Type: KeyRune, Rune: '?'})
	if !handled {
		t.Fatal("expected '?' shortcut to be handled when composer is empty")
	}

	// 2. When composer is non-empty:
	w.composer.SetText("test")
	handled = w.handleShortcut(ctx, KeyEvent{Type: KeyRune, Rune: 'd'})
	if handled {
		t.Fatal("expected 'd' shortcut NOT to be handled when composer is non-empty")
	}
	w.composer.HandleKey(KeyEvent{Type: KeyRune, Rune: 'd'})
	if w.composer.Text() != "testd" {
		t.Fatalf("expected 'd' to be typed into composer, got: %q", w.composer.Text())
	}

	handled = w.handleShortcut(ctx, KeyEvent{Type: KeyRune, Rune: '?'})
	if handled {
		t.Fatal("expected '?' shortcut NOT to be handled when composer is non-empty")
	}
	w.composer.HandleKey(KeyEvent{Type: KeyRune, Rune: '?'})
	if w.composer.Text() != "testd?" {
		t.Fatalf("expected '?' to be typed into composer, got: %q", w.composer.Text())
	}
}
