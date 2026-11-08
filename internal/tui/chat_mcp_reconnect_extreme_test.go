//go:build !governed

package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// Simulate /quit without terminating the provider pane, then start a workspace
// with no volatile launch leases, as a newly installed TUI binary would do.
func restartedChatWorkspace(t *testing.T, old *Workspace) *Workspace {
	t.Helper()
	// The existing fake tmux's display-message PID is a placeholder; retain
	// the PID from its pane inventory, as recovery's anti-reuse fence expects.
	panes, err := tmux.ListPanes(t.Context(), old.tmuxSession)
	if err != nil {
		t.Fatal(err)
	}
	old.tmuxMu.Lock()
	for _, p := range panes {
		if a := old.tmuxActiveWins["marshal-chat"]; a != nil && a.paneID == p.PaneID {
			a.pid = p.PID
			if err := old.saveChatBindingForAgent(old.providerRoot(), a); err != nil {
				t.Fatal(err)
			}
		}
	}
	old.tmuxMu.Unlock()
	stopRecoveryDraftWatcher(old)
	old.closeProposalMCP()
	old.tmuxMu.Lock()
	for _, a := range old.tmuxActiveWins {
		a.cancel()
	}
	old.tmuxMu.Unlock()
	old.tmuxMonitors.Wait()
	w := NewWorkspace(old.store, old.projectID, "SESSION-new-runtime")
	w.runtime, w.workDir = old.runtime, old.workDir
	w.tmuxSession, w.tmuxPath = old.tmuxSession, old.tmuxPath
	w.tmuxMarshalWin, w.tmuxMarshalWinID = old.tmuxMarshalWin, old.tmuxMarshalWinID
	w.tmuxActiveWins = make(map[string]*activeTmuxAgent)
	cleanupTmuxWorkspace(t, w)
	t.Cleanup(w.closeProposalMCP)
	return w
}

func TestChatMCPRuntimeRestartResumesExactConversation(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			old := recoveryChatWorkspace(t)
			marshalSwitchEnvironment(t)
			root := old.providerRoot()
			if err := saveDefaultProvider(root, provider); err != nil {
				t.Fatal(err)
			}
			if err := old.startMarshalChat(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			if err := old.captureChatConversation(root, "exact-conversation"); err != nil {
				t.Fatal(err)
			}
			a := old.tmuxActiveWins["marshal-chat"]
			oldLaunch := old.proposalLaunchID()
			oldPath := nativeMCPChildConfigPath(t, provider, a.args, a.env, a.briefingDir)
			cfg, err := proposalmcp.LoadConfig(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			oldChild := memoryChildWithConfigPath(t, root, oldPath)
			oldChild.call(t, "marshal_status", map[string]any{}, true)
			w := restartedChatWorkspace(t, old)
			oldChild.call(t, "marshal_proposal_submit", map[string]any{"submission_id": "stale", "proposal": map[string]string{"action": "setting", "key": "acceptance-mode", "value": "marshal"}}, false)
			w.recoverSurvivingWorkers(root)
			w.tmuxMu.Lock()
			a = copyAgentLocked(w.tmuxActiveWins["marshal-chat"])
			w.tmuxMu.Unlock()
			if a == nil || a.briefingDir == nil {
				t.Fatal("surviving chat MCP binding was not restored")
			}
			if a.sessionID != "exact-conversation" || !strings.Contains(strings.Join(a.args, " "), "exact-conversation") {
				t.Fatalf("conversation lost: %+v", a)
			}
			binding := loadChatBinding(root)
			if binding.MCPLaunchID == "" || binding.MCPLaunchID != w.proposalLaunchID() || binding.MCPLaunchID == oldLaunch {
				t.Fatalf("runtime launch was not durably rebound: %+v", binding)
			}
			path := nativeMCPChildConfigPath(t, provider, a.args, a.env, a.briefingDir)
			fresh, err := proposalmcp.LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Endpoint == fresh.Endpoint || cfg.Capability == fresh.Capability {
				t.Fatal("stale authority reused")
			}
			child := memoryChildWithConfigPath(t, root, path)
			raw := child.call(t, "marshal_proposal_submit", map[string]any{"submission_id": "reconnected", "proposal": map[string]string{"action": "setting", "key": "acceptance-mode", "value": "marshal"}}, true)
			var receipt proposalMCPReceipt
			if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.State != "admitted" {
				t.Fatalf("reconnected proposal: %+v", receipt)
			}
			batch := w.permissions.queue.Take(false)
			if len(batch) != 1 || batch[0].SourceLaunch != w.proposalLaunchID() {
				t.Fatalf("lost owner gating: %+v", batch)
			}
			if !w.routeBuildPermission(t.Context(), batch[0]) {
				t.Fatal("reconnected proposal cannot use normal authority pipeline")
			}
			first := w.proposalLaunchID()
			w.recoverSurvivingWorkers(root)
			if w.proposalLaunchID() != first {
				t.Fatal("repeated recovery restarted a healthy chat")
			}
		})
	}
}

func TestChatMCPRecoveryRefusesForeignBinding(t *testing.T) {
	for _, mismatch := range []string{"project", "session", "pane", "provider"} {
		t.Run(mismatch, func(t *testing.T) {
			w := recoveryChatWorkspace(t)
			root := w.providerRoot()
			a := &activeTmuxAgent{provider: "codex", paneID: "%1", sessionID: "exact"}
			saved := chatBinding{Provider: "codex", Project: tmux.ProjectHash(root), TmuxSession: w.tmuxSession, PaneID: "%1", SessionID: "exact"}
			switch mismatch {
			case "project":
				saved.Project = "other-project"
			case "session":
				saved.TmuxSession = "other-session"
			case "pane":
				saved.PaneID = "%2"
			case "provider":
				saved.Provider = "claude"
			}
			if err := w.restoreRecoveredChatMCP(t.Context(), root, a, saved); err == nil {
				t.Fatal("foreign binding was accepted")
			}
			if w.proposalLaunchID() != "" {
				t.Fatal("foreign chat received MCP authority")
			}
		})
	}
}

func TestChatMCPRuntimeRestartFailureIsVisible(t *testing.T) {
	w := recoveryChatWorkspace(t)
	root := w.providerRoot()
	win := tmux.ChatWindowName(root)
	if err := tmux.NewWindow(t.Context(), w.tmuxSession, win, root, nil, []string{"codex"}); err != nil {
		t.Fatal(err)
	}
	if err := saveChatBinding(root, chatBinding{Provider: "codex", Binary: "codex"}); err != nil {
		t.Fatal(err)
	}
	if err := saveAgentRecord(root, w.tmuxSession, &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", provider: "codex", paneID: "%1", window: win, pid: 101}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.closeProposalMCP)
	w.recoverSurvivingWorkers(w.providerRoot())
	note, err := os.ReadFile(inboxPath(w.providerRoot(), "marshal"))
	if err != nil || !strings.Contains(string(note), "/exit") || !strings.Contains(string(note), "/marshal chat") {
		t.Fatalf("missing chat repair note: %s %v", note, err)
	}
	w.mu.Lock()
	output := w.state.LastOutput
	w.mu.Unlock()
	if !strings.Contains(output, "MCP") || !strings.Contains(output, "/marshal chat") {
		t.Fatalf("silent recovery failure: %s", output)
	}
	if w.proposalLaunchID() != "" {
		t.Fatal("unknown conversation received authority")
	}
	if _, err := w.marshalChat(t.Context()); err == nil || !strings.Contains(err.Error(), "/exit") {
		t.Fatalf("startup or explicit reopen bypassed the recovery refusal: %v", err)
	}
}
