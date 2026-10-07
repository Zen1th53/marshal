package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func marshalSwitchEnvironment(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	for name, version := range map[string]string{"codex": "codex-cli 0.159.2", "claude": "2.1.286 (Claude Code)", "agy": "1.2.7"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho '"+version+"'\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
}

func TestMarshalModelSwitchPersistsAndRestarts(t *testing.T) {
	for _, attached := range []bool{false, true} {
		t.Run(map[bool]string{false: "startup", true: "runtime"}[attached], func(t *testing.T) {
			_, logFile := setupFakeTmux(t)
			marshalSwitchEnvironment(t)
			var w *Workspace
			if attached {
				_, w, _ = acceptanceWorkspace(t)
			} else {
				w = NewWorkspace(nil, "project", "test-session")
				w.workDir = t.TempDir()
			}
			cleanupTmuxWorkspace(t, w)
			root := w.providerRoot()
			w.tmuxSession = "test-session"
			w.tmuxMarshalWin = "marshal"
			w.tmuxPath = "tmux"
			w.tmuxActiveWins = make(map[string]*activeTmuxAgent)
			if err := saveDefaultProvider(root, "codex"); err != nil {
				t.Fatal(err)
			}
			if err := w.startMarshalChat(context.Background(), root); err != nil {
				t.Fatal(err)
			}
			old := w.tmuxActiveWins["marshal-chat"]
			oldPane := old.paneID
			// A provider change must never resume a conversation from another CLI.
			w.tmuxMu.Lock()
			old.sessionID = "codex-only-session"
			if err := w.saveChatBindingForAgent(root, old); err != nil {
				t.Fatal(err)
			}
			w.tmuxMu.Unlock()
			if err := os.WriteFile(logFile, nil, 0600); err != nil {
				t.Fatal(err)
			}
			msg, err := w.ExecuteCommand(context.Background(), "/marshal model claude")
			if err != nil || msg != "The Marshal now uses claude." {
				t.Fatalf("switch: %q %v", msg, err)
			}
			chat := w.tmuxActiveWins["marshal-chat"]
			if chat == nil || chat == old || chat.provider != "claude" || chat.paneID == oldPane || chat.window != old.window {
				t.Fatal("chat not replaced with a new Claude pane in the same window")
			}
			if loadDefaultProvider(root) != "claude" {
				t.Fatal("choice not persisted")
			}
			select {
			case <-old.doneChan:
			default:
				t.Fatal("old monitor still running")
			}
			if chat.sessionID != "" || strings.Contains(strings.Join(chat.args, " "), "codex-only-session") {
				t.Fatal("resumed the old provider conversation")
			}
			if !strings.Contains(strings.Join(chat.args, " "), "--append-system-prompt") {
				t.Fatal("hidden protocol missing")
			}
			data, _ := os.ReadFile(logFile)
			log := string(data)
			kill := strings.Index(log, "kill-pane -t "+oldPane)
			launch := strings.Index(log, "new-window -d -t test-session -n "+old.window)
			if kill < 0 || launch < kill {
				t.Fatalf("stop/launch order: %s", log)
			}
			if strings.Contains(log, "select-window -t test-session:"+old.window) || !strings.Contains(log, "select-window -t test-session:marshal") {
				t.Fatalf("control centre focus lost: %s", log)
			}
			if err := os.WriteFile(logFile, nil, 0600); err != nil {
				t.Fatal(err)
			}
			msg, err = w.ExecuteCommand(context.Background(), "/marshal model claude")
			if err != nil || msg != "The Marshal now uses claude." || w.tmuxActiveWins["marshal-chat"] != chat {
				t.Fatalf("same provider: %q %v", msg, err)
			}
			data, _ = os.ReadFile(logFile)
			if strings.Contains(string(data), "kill-pane") || strings.Contains(string(data), "new-window") {
				t.Fatal("same provider changed pane")
			}
			if attached {
				if _, err := w.marshalChat(context.WithValue(context.Background(), tmuxBackgroundLaunchKey{}, true)); err != nil {
					t.Fatalf("matching chat: %v", err)
				}
				if w.tmuxActiveWins["marshal-chat"] != chat {
					t.Fatal("matching chat replaced pane")
				}
			}
			// Reattachment adopts the new provider without launching or relabelling it.
			reattached := NewWorkspace(nil, "project", "test-session")
			reattached.workDir = root
			cleanupTmuxWorkspace(t, reattached)
			reattached.InitTmux(root)
			if a := reattached.tmuxActiveWins["marshal-chat"]; a == nil || a.provider != "claude" {
				t.Fatal("reattach lost provider")
			}
			// A new workspace with no surviving chat uses the persisted choice.
			fresh := NewWorkspace(nil, "project", "test-session")
			fresh.workDir = root
			fresh.tmuxSession = "test-session"
			fresh.tmuxActiveWins = make(map[string]*activeTmuxAgent)
			cleanupTmuxWorkspace(t, fresh)
			if err := fresh.startMarshalChat(context.Background(), root); err != nil {
				t.Fatal(err)
			}
			if fresh.tmuxActiveWins["marshal-chat"].provider != "claude" {
				t.Fatal("restart lost provider")
			}
		})
	}
}

func TestMarshalModelMissingCLILeavesChatRunning(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	w := NewWorkspace(nil, "project", "test-session")
	w.workDir = t.TempDir()
	old := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", provider: "codex", paneID: "%1", cancel: func() { t.Error("old chat cancelled") }}
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": old}
	if err := saveDefaultProvider(w.workDir, "codex"); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"claude", "agy"} {
		_, err := w.ExecuteCommand(context.Background(), "/marshal model "+provider)
		if err == nil || !strings.Contains(err.Error(), provider+" CLI is missing") {
			t.Fatalf("missing CLI: %v", err)
		}
		if w.tmuxActiveWins["marshal-chat"] != old || loadDefaultProvider(w.workDir) != "codex" {
			t.Fatal("missing CLI changed chat/default")
		}
	}
	data, _ := os.ReadFile(logFile)
	if strings.Contains(string(data), "kill-pane") || strings.Contains(string(data), "new-window") {
		t.Fatal("missing CLI touched pane")
	}
}

func TestMarshalChatProviderAliasReusesPane(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	w := NewWorkspace(nil, "project", "test-session")
	root := t.TempDir()
	w.tmuxSession = "test-session"
	window := tmux.ChatWindowName(root)
	if err := tmux.NewWindow(context.Background(), w.tmuxSession, window, root, nil, []string{"echo", "chat"}); err != nil {
		t.Fatal(err)
	}
	chat := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", provider: "agy", window: window, paneID: "%1"}
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": chat}
	if err := os.WriteFile(logFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	msg, err := w.runNativeAgentInTmux(context.Background(), nativeLaunchOperator, "antigravity", "Antigravity", root, "echo", nil, nil, nil, nil, nil, nil, nil, nil, true)
	if err != nil || !strings.Contains(msg, "Switched to active") || w.tmuxActiveWins["marshal-chat"] != chat || chat.provider != "agy" {
		t.Fatalf("alias reuse: %q %v", msg, err)
	}
	data, _ := os.ReadFile(logFile)
	if strings.Contains(string(data), "new-window") || strings.Contains(string(data), "kill-pane") {
		t.Fatal("matching alias replaced pane")
	}
}

func TestMarshalChatCancelledRecoveryDoesNotRespawn(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	w := NewWorkspace(nil, "project", "test-session")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.restartMarshalChat(ctx, &activeTmuxAgent{provider: "codex", paneID: "%old", window: "old"}, t.TempDir())
	data, _ := os.ReadFile(logFile)
	if strings.Contains(string(data), "respawn-window") || strings.Contains(string(data), "respawn-pane") || strings.Contains(string(data), "new-window") {
		t.Fatal("cancelled old monitor respawned a chat")
	}
}

func TestMarshalChatStartupUsesPersistedProvider(t *testing.T) {
	setupFakeTmux(t)
	marshalSwitchEnvironment(t)
	_, w, _ := acceptanceWorkspace(t)
	cleanupTmuxWorkspace(t, w)
	root := w.providerRoot()
	// The attached project owns the default, even if the workspace directory
	// or the last chat's saved provider differs.
	w.workDir = t.TempDir()
	if err := saveDefaultProvider(root, "claude"); err != nil {
		t.Fatal(err)
	}
	if err := saveChatBinding(root, chatBinding{Provider: "codex", SessionID: "old-codex"}); err != nil {
		t.Fatal(err)
	}
	w.InitTmux(root)
	w.tmuxMu.Lock()
	chat := copyAgentLocked(w.tmuxActiveWins["marshal-chat"])
	w.tmuxMu.Unlock()
	if chat == nil || chat.provider != "claude" || chat.sessionID != "" || strings.Contains(strings.Join(chat.args, " "), "old-codex") {
		t.Fatal("startup did not use the attached project's persisted Claude provider")
	}
}
