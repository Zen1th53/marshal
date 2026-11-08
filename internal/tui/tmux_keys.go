package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/statepath"
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
	w.tmuxMu.Lock()
	target := w.marshalTarget()
	w.tmuxMu.Unlock()
	return w.bindKeyTable(ctx, pane, workspaceKeyTable(root), target, "select-window", "-t", target, ";", "send-keys", "-t", target, "F11")
}

// workspaceKeyTable is the key table of the project's agent panes.
func workspaceKeyTable(root string) string {
	return statepath.Name + "-keys-" + tmux.ProjectHash(root)
}

// dockKeyTable is the key table of the Marshal chat pane while it is docked
// in the Board window. It exists only while the dock does.
func dockKeyTable(root string) string {
	return statepath.Name + "-dock-" + tmux.ProjectHash(root)
}

// boardDockKeyTable is the key table of the Board pane while the chat is
// docked beside it. It exists only while the dock does; the Board pane has
// the agent key table before and after.
func boardDockKeyTable(root string) string {
	return statepath.Name + "-board-" + tmux.ProjectHash(root)
}

// dockSwitchKeys are what move the keyboard between the docked chat and the
// Board. tmux runs them, so neither the provider CLI nor the Board ever sees
// them: F11 toggles from either side, and Alt+arrow moves toward the other
// pane (the chat is on the left). Alt+arrow away from the other pane is not
// bound and stays the CLI's or the composer's word move.
const (
	dockToggleKey = "F11"
	dockToBoard   = "M-Right"
	dockToChat    = "M-Left"
)

// bindDockKeys gives the docked chat pane its own table: F11 and Alt+Right
// select the Board pane beside it instead of switching windows, and the
// provider and stop keys reach the Board as they do from any agent pane.
// Every other key is unbound in the table and so reaches the chat.
func (w *Workspace) bindDockKeys(ctx context.Context, chat, board, root string) error {
	table := dockKeyTable(root)
	if err := w.bindKeyTable(ctx, chat, table, board, "select-pane", "-t", board); err != nil {
		return err
	}
	return tmux.BindWindowKey(ctx, chat, table, dockToBoard, "select-pane", "-t", board)
}

// bindBoardDockKeys gives the Board pane its own table while the chat is
// docked: F11 and Alt+Left select the chat. Everything else, F11 from a
// worker's window included, still reaches the Board as before.
func (w *Workspace) bindBoardDockKeys(ctx context.Context, board, chat, root string) error {
	table := boardDockKeyTable(root)
	if err := w.bindKeyTable(ctx, board, table, board, "select-pane", "-t", chat); err != nil {
		return err
	}
	return tmux.BindWindowKey(ctx, board, table, dockToChat, "select-pane", "-t", chat)
}

// bindKeyTable binds the control-centre keys of pane in table: the provider
// keys not yet open and the stop keys are sent to target, F11 runs f11. The
// server's mouse bindings are copied in, so a click still selects a pane and
// the wheel still scrolls while the table is the client's.
func (w *Workspace) bindKeyTable(ctx context.Context, pane, table, target string, f11 ...string) error {
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		w.tmuxMu.Lock()
		active := w.tmuxActiveWins[provider] != nil
		w.tmuxMu.Unlock()
		if !active {
			key := providerFKey(provider)
			if err := tmux.BindWindowKey(ctx, pane, table, key, "send-keys", "-t", target, key); err != nil {
				return err
			}
		}
	}
	if err := tmux.BindWindowKey(ctx, pane, table, dockToggleKey, f11...); err != nil {
		return err
	}
	if err := tmux.BindWindowKey(ctx, pane, table, "C-x", "send-keys", "-t", target, "C-x"); err != nil {
		return err
	}
	if err := tmux.BindWindowKey(ctx, pane, table, "M-x", "send-keys", "-t", target, "C-x"); err != nil {
		return err
	}
	return tmux.BindMouseKeys(ctx, table)
}

// A snapshot has its own zero mutex; mutable fields are read under tmuxMu.
func copyAgentLocked(a *activeTmuxAgent) *activeTmuxAgent {
	if a == nil {
		return nil
	}
	return &activeTmuxAgent{launchOrigin: a.launchOrigin, canonicalTaskID: a.canonicalTaskID, executionRunID: a.executionRunID, id: a.id, role: a.role, provider: a.provider, taskID: a.taskID, label: a.label, window: a.window, windowID: a.windowID, paneID: a.paneID, pid: a.pid, pgid: a.pgid, state: a.state, readOnly: a.readOnly, isJoined: a.isJoined, cancel: a.cancel, briefingDir: a.briefingDir, doneChan: a.doneChan, binary: a.binary, args: a.args, env: a.env, sessionID: a.sessionID, mcpLaunchID: a.mcpLaunchID, historyPath: a.historyPath, historyBaseline: a.historyBaseline, runID: a.runID, driver: a.driver, handle: a.handle, supervisor: a.supervisor}
}
