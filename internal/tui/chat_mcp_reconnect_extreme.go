//go:build !governed

package tui

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func (w *Workspace) restoreRecoveredChatMCP(ctx context.Context, root string, a *activeTmuxAgent, saved chatBinding) error {
	if w.runtime == nil || (a.provider != "codex" && a.provider != "claude" && a.provider != "opencode") {
		return nil
	}
	// Recovery already checked the durable agent record against the live pane's
	// project/session/PID. Check the conversation binding before minting authority.
	// Legacy bindings have no scope fields; their project-local agent record and
	// exact captured session remain required, never --last or a history guess.
	if !tmux.SessionBelongs(w.tmuxSession, root) || canonicalNeutralProvider(saved.Provider) != canonicalNeutralProvider(a.provider) ||
		saved.Project != "" && saved.Project != tmux.ProjectHash(root) ||
		saved.TmuxSession != "" && saved.TmuxSession != w.tmuxSession ||
		saved.PaneID != "" && saved.PaneID != a.paneID {
		return errChatMCPBindingMismatch
	}
	if saved.SessionID == "" || saved.SessionID != a.sessionID {
		return errors.New("the exact provider conversation ID has not been captured; automatic resume is unsafe")
	}
	// A recovered pane has no lease in this workspace, even if its old broker
	// socket is still on disk after a crash. Never adopt that capability.
	binary, err := project.FindBinary(a.provider)
	if err != nil {
		return err
	}
	w.tmuxMu.Lock()
	a.binary = binary // Resolve the installed provider again; saved binaries can move.
	w.tmuxMu.Unlock()
	if err := w.respawnMarshalChat(ctx, a, root, false); err != nil {
		return err
	}
	w.RecordActivity("Marshal chat MCP restored; resumed the saved " + a.provider + " conversation.")
	return nil
}
