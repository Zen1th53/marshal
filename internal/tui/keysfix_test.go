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
