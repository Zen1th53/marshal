package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/tmux"
	"strings"
)

// Caller holds tmuxMu. Targets are immutable pane IDs when known.
func (w *Workspace) marshalTarget() string {
	if w.tmuxMarshalPaneID != "" {
		return w.tmuxMarshalPaneID
	}
	if w.tmuxMarshalWinID != "" {
		return w.tmuxMarshalWinID
	}
	if strings.HasPrefix(w.tmuxMarshalWin, w.tmuxSession+":") {
		return w.tmuxMarshalWin
	}
	return w.tmuxSession + ":" + w.tmuxMarshalWin
}
func (w *Workspace) bindWorkspaceKeys(ctx context.Context, pane, root string) error {
	if pane == "" {
		return nil
	}
	table := "marshal-keys-" + tmux.ProjectHash(root)
	w.tmuxMu.Lock()
	target := w.marshalTarget()
	w.tmuxMu.Unlock()
	if err := tmux.BindWindowKey(ctx, pane, table, "F11", "select-window", "-t", target); err != nil {
		return err
	}
	if err := tmux.BindWindowKey(ctx, pane, table, "C-x", "send-keys", "-t", target, "C-x"); err != nil {
		return err
	}
	return tmux.BindWindowKey(ctx, pane, table, "M-x", "send-keys", "-t", target, "C-x")
}

// A snapshot has its own zero mutex; mutable fields are read under tmuxMu.
func copyAgentLocked(a *activeTmuxAgent) *activeTmuxAgent {
	if a == nil {
		return nil
	}
	return &activeTmuxAgent{launchOrigin: a.launchOrigin, canonicalTaskID: a.canonicalTaskID, executionRunID: a.executionRunID, id: a.id, role: a.role, provider: a.provider, taskID: a.taskID, label: a.label, window: a.window, windowID: a.windowID, paneID: a.paneID, pid: a.pid, pgid: a.pgid, state: a.state, readOnly: a.readOnly, isJoined: a.isJoined, cancel: a.cancel, briefingDir: a.briefingDir, doneChan: a.doneChan, binary: a.binary, args: a.args, env: a.env, sessionID: a.sessionID, historyBaseline: a.historyBaseline, runID: a.runID, driver: a.driver, handle: a.handle, supervisor: a.supervisor}
}
