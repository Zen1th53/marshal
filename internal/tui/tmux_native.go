package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/tmux"
)

type activeTmuxAgent struct {
	id          string // e.g. "codex", "claude", "marshal-chat", "task-01"
	role        string // "worker", "marshal-chat", "task"
	provider    string
	taskID      string
	label       string
	window      string
	windowID    string
	paneID      string
	pid         int
	pgid        int
	state       string // "working", "waiting", "done", "failed"
	readOnly    bool
	isJoined    bool
	cancel      context.CancelFunc
	briefingDir *briefingDir
	doneChan    chan struct{}
}

// InitTmux initializes tmux session and window awareness for the workspace.
func (w *Workspace) InitTmux(root ...string) {
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()

	projectRoot := w.workDir
	if len(root) > 0 && root[0] != "" {
		projectRoot = root[0]
	}
	if w.runtime != nil && w.runtime.ProjectRoot() != "" {
		projectRoot = w.runtime.ProjectRoot()
	}

	bin, err := tmux.FindBinary()
	if err != nil {
		return
	}
	w.tmuxPath = bin

	if w.tmuxActiveWins == nil {
		w.tmuxActiveWins = make(map[string]*activeTmuxAgent)
	}

	sessionName := tmux.SessionName(projectRoot)
	if tmux.IsInsideTmux() {
		sess, win, winID, err := tmux.CurrentSessionAndWindow(context.Background())
		if err == nil && sess != "" {
			w.tmuxSession = sess
			if win != "" {
				w.tmuxMarshalWin = win
				w.tmuxMarshalWinID = winID
			} else {
				w.tmuxMarshalWin = "marshal"
			}
		} else {
			w.tmuxSession = sessionName
			w.tmuxMarshalWin = "marshal"
		}
		paneID, _ := tmux.CurrentPaneID(context.Background())
		w.tmuxMarshalPaneID = paneID

		_ = tmux.BindGlobalKey(context.Background(), "F11", "select-window", "-t", w.tmuxMarshalWin)
		_ = tmux.BindGlobalKey(context.Background(), "M-x", "send-keys", "-t", w.tmuxMarshalWin, "/stop all", "Enter")
	} else {
		w.tmuxSession = sessionName
		w.tmuxMarshalWin = "marshal"
	}

	// Adopt surviving windows from existing session if re-attaching
	w.adoptSurvivingWorkersLocked(projectRoot)
}

func (w *Workspace) adoptSurvivingWorkersLocked(projectRoot string) {
	if w.tmuxSession == "" {
		return
	}
	panes, err := tmux.ListPanes(context.Background(), w.tmuxSession)
	if err != nil {
		return
	}
	hash := tmux.ProjectHash(projectRoot)
	for _, p := range panes {
		if p.Dead || p.WindowName == w.tmuxMarshalWin {
			continue
		}
		// Match marshal-chat-<hash>
		if p.WindowName == fmt.Sprintf("marshal-chat-%s", hash) {
			if _, ok := w.tmuxActiveWins["marshal-chat"]; !ok {
				agentCtx, cancel := context.WithCancel(context.Background())
				agent := &activeTmuxAgent{
					id:       "marshal-chat",
					role:     "marshal-chat",
					label:    "Marshal Chat",
					window:   p.WindowName,
					windowID: p.WindowID,
					paneID:   p.PaneID,
					pid:      p.PID,
					state:    "working",
					readOnly: true,
					cancel:   cancel,
				}
				w.tmuxActiveWins["marshal-chat"] = agent
				w.monitorAgent(agentCtx, agent, projectRoot, nil, nil, nil, nil, nil)
			}
			continue
		}
		// Match marshal-<provider>-<hash> or marshal-task-<taskID>-<hash>
		prefix := "marshal-"
		suffix := "-" + hash
		if strings.HasPrefix(p.WindowName, prefix) && strings.HasSuffix(p.WindowName, suffix) {
			targetPart := strings.TrimSuffix(strings.TrimPrefix(p.WindowName, prefix), suffix)
			if strings.HasPrefix(targetPart, "task-") {
				taskID := strings.TrimPrefix(targetPart, "task-")
				agentID := "task-" + taskID
				if _, ok := w.tmuxActiveWins[agentID]; !ok {
					agentCtx, cancel := context.WithCancel(context.Background())
					agent := &activeTmuxAgent{
						id:       agentID,
						role:     "task",
						taskID:   taskID,
						label:    fmt.Sprintf("Task %s", taskID),
						window:   p.WindowName,
						windowID: p.WindowID,
						paneID:   p.PaneID,
						pid:      p.PID,
						state:    "working",
						readOnly: true,
						cancel:   cancel,
					}
					w.tmuxActiveWins[agentID] = agent
					w.monitorAgent(agentCtx, agent, projectRoot, nil, nil, nil, nil, nil)
				}
			} else if targetPart != "" {
				provider := targetPart
				if _, ok := w.tmuxActiveWins[provider]; !ok {
					agentCtx, cancel := context.WithCancel(context.Background())
					agent := &activeTmuxAgent{
						id:       provider,
						role:     "worker",
						provider: provider,
						label:    strings.Title(provider),
						window:   p.WindowName,
						windowID: p.WindowID,
						paneID:   p.PaneID,
						pid:      p.PID,
						state:    "working",
						readOnly: true,
						cancel:   cancel,
					}
					w.tmuxActiveWins[provider] = agent
					w.monitorAgent(agentCtx, agent, projectRoot, nil, nil, nil, nil, nil)
				}
			}
		}
	}
}

// isTmuxActive reports whether tmux is available and MARSHAL is connected to it.
func (w *Workspace) isTmuxActive() bool {
	w.tmuxMu.Lock()
	path := w.tmuxPath
	w.tmuxMu.Unlock()
	if path == "" {
		w.InitTmux("")
		w.tmuxMu.Lock()
		path = w.tmuxPath
		w.tmuxMu.Unlock()
	}
	return path != "" && (tmux.IsInsideTmux() || os.Getenv("MARSHAL_TEST_FORCE_TMUX") == "1")
}

func providerFKey(provider string) string {
	switch provider {
	case "codex":
		return "F7"
	case "claude":
		return "F8"
	case "opencode":
		return "F9"
	case "antigravity", "agy":
		return "F12"
	default:
		return ""
	}
}

// runNativeAgentInTmux launches or switches to a native agent in its dedicated tmux window.
func (w *Workspace) runNativeAgentInTmux(
	ctx context.Context,
	provider, label, root, binary string,
	args []string,
	briefingEnv []string,
	dir *briefingDir,
	watch *nativeHistoryWatch,
	peers []*nativeHistoryWatch,
	chStream *stream,
	view *inboxView,
	briefingNotes []string,
	isMarshalChat ...bool,
) (string, error) {
	isChat := len(isMarshalChat) > 0 && isMarshalChat[0]

	agentID := provider
	role := "worker"
	winName := tmux.WindowName(provider, root)
	agentLabel := label

	if isChat {
		agentID = "marshal-chat"
		role = "marshal-chat"
		winName = tmux.ChatWindowName(root)
		agentLabel = "Marshal Chat (" + label + ")"
	}

	w.tmuxMu.Lock()
	if w.tmuxActiveWins == nil {
		w.tmuxActiveWins = make(map[string]*activeTmuxAgent)
	}
	existingAgent, exists := w.tmuxActiveWins[agentID]
	w.tmuxMu.Unlock()

	winExists, _ := tmux.WindowExists(ctx, w.tmuxSession, winName)
	if exists && winExists {
		// Window already exists; switch to it without restarting the process.
		if existingAgent != nil && existingAgent.isJoined {
			_ = tmux.SelectPane(ctx, existingAgent.paneID)
		} else {
			_ = tmux.SelectWindow(ctx, winName)
		}
		if !isChat {
			w.nativeProvider = provider
		}
		w.updateTmuxStatusLine(ctx)
		return fmt.Sprintf("Switched to active %s session (tmux window %s). The session continues running. Press F11 to return to MARSHAL.", agentLabel, winName), nil
	}

	// Create new window using direct argv execution with safe env launcher
	cmd := append([]string{binary}, args...)
	if err := tmux.NewWindow(ctx, w.tmuxSession, winName, root, briefingEnv, cmd); err != nil {
		return "", fmt.Errorf("launch %s in tmux: %w", agentLabel, err)
	}

	// Set remain-on-exit on so dead pane can be inspected and captured before cleanup
	_ = tmux.SetWindowOption(ctx, winName, "remain-on-exit", "on")

	// Set pane view-only by default (Decision 4)
	_ = tmux.SetPaneReadOnly(ctx, winName, true)

	// Switch to the newly opened window
	_ = tmux.SelectWindow(ctx, winName)

	// Discover immutable pane ID and PID
	panes, _ := tmux.ListPanes(ctx, w.tmuxSession)
	paneID := winName
	pid := 0
	pgid := 0
	windowID := ""
	for _, p := range panes {
		if p.WindowName == winName {
			paneID = p.PaneID
			windowID = p.WindowID
			pid = p.PID
			if pid > 0 {
				pidVal, pgidVal, err := tmux.PanePIDAndPGID(ctx, paneID)
				if err == nil {
					pid, pgid = pidVal, pgidVal
				}
			}
			break
		}
	}

	// Bind provider's F-key and F11 in tmux (for workers only, not chat)
	if !isChat {
		fkey := providerFKey(provider)
		if fkey != "" {
			_ = tmux.BindGlobalKey(ctx, fkey, "select-window", "-t", winName)
		}
	}
	_ = tmux.BindGlobalKey(ctx, "F11", "select-window", "-t", w.tmuxMarshalWin)

	agentCtx, cancel := context.WithCancel(context.Background())
	agent := &activeTmuxAgent{
		id:          agentID,
		role:        role,
		provider:    provider,
		label:       agentLabel,
		window:      winName,
		windowID:    windowID,
		paneID:      paneID,
		pid:         pid,
		pgid:        pgid,
		state:       "working",
		readOnly:    true,
		cancel:      cancel,
		briefingDir: dir,
		doneChan:    make(chan struct{}),
	}

	w.tmuxMu.Lock()
	w.tmuxActiveWins[agentID] = agent
	if !isChat {
		w.nativeProvider = provider
	}
	w.tmuxMu.Unlock()
	w.updateTmuxStatusLine(ctx)

	w.monitorAgent(agentCtx, agent, root, dir, watch, peers, chStream, view)

	msg := fmt.Sprintf("Opened native %s in tmux window %s (view-only mode). Press F11 to return to MARSHAL. Use /takeover to enable typing.", agentLabel, winName)
	return msg, nil
}

func (w *Workspace) monitorAgent(
	agentCtx context.Context,
	agent *activeTmuxAgent,
	root string,
	dir *briefingDir,
	watch *nativeHistoryWatch,
	peers []*nativeHistoryWatch,
	chStream *stream,
	view *inboxView,
) {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-agentCtx.Done():
				return
			case <-ticker.C:
				// Poll syncs
				if watch != nil && capturesLive(agent.provider) {
					_ = watch.sync()
				}
				for _, pw := range peers {
					_ = pw.sync()
				}
				if chStream != nil && view != nil {
					positions, _ := loadCursors(root)
					channelCfg, _ := loadChannelConfig(root)
					if entries, err := chStream.since(positions[agent.provider]); err == nil && len(entries) > 0 {
						_, _ = view.deliver(entries, channelCfg)
						positions[agent.provider] = entries[len(entries)-1].Seq
						_ = saveCursors(root, positions)
					}
				}
				if view != nil {
					status := liveStatus{Delivered: view.Count(), LastSync: time.Now().UTC()}
					_ = writeLiveStatus(root, agent.provider, status)
				}

				// Check whether pane is dead
				dead, exitCode, err := tmux.PaneDeadStatus(context.Background(), agent.paneID)
				if err != nil {
					// Fallback to checking window existence if pane query failed
					exists, _ := tmux.WindowExists(context.Background(), w.tmuxSession, agent.window)
					if !exists {
						dead = true
					}
				}

				if dead {
					// Final sync
					if watch != nil {
						_ = watch.sync()
					}
					for _, pw := range peers {
						_ = pw.sync()
					}

					// Capture pane output while the pane is still alive in dead state
					evidence, _ := tmux.CapturePane(context.Background(), agent.paneID)
					if evidence == "" {
						evidence, _ = tmux.CapturePane(context.Background(), agent.window)
					}
					if evidence != "" {
						_ = saveAgentEvidence(root, agent.id, evidence)
					}
					if dir != nil {
						dir.remove()
					}

					// Terminate process group if lingering
					tmux.KillProcessGroup(agent.pid, agent.pgid)

					// Explicitly close the finished pane / window
					_ = tmux.KillPane(context.Background(), agent.paneID)
					_ = tmux.KillWindow(context.Background(), agent.window)

					w.tmuxMu.Lock()
					delete(w.tmuxActiveWins, agent.id)
					w.tmuxMu.Unlock()

					if agent.role != "marshal-chat" {
						if fkey := providerFKey(agent.provider); fkey != "" {
							_ = tmux.UnbindGlobalKey(context.Background(), fkey)
						}
					}

					w.RecordActivity(fmt.Sprintf("%s session ended (exit %d). Output saved to evidence.", agent.label, exitCode))
					w.updateTmuxStatusLine(context.Background())
					return
				}

				// Check if agent is waiting for user input
				isWaiting := false
				if agent.role == "worker" {
					if w.marshalNativeApprovalWaiting(nil, marshal.Run{}) {
						isWaiting = true
					}
				}
				if isWaiting && agent.state != "waiting" {
					agent.state = "waiting"
					w.updateTmuxStatusLine(context.Background())
					w.RecordActivity(fmt.Sprintf("%s is waiting for your input.", agent.label))
					if w.tmuxFollowActive {
						_ = tmux.SelectWindow(context.Background(), agent.window)
					}
				} else if !isWaiting && agent.state == "waiting" {
					agent.state = "working"
					w.updateTmuxStatusLine(context.Background())
				}
			}
		}
	}()
}

// RecordActivity records activity into workspace state and triggers a redraw if interactive.
func (w *Workspace) RecordActivity(msg string) {
	w.mu.Lock()
	if w.state.LastOutput != "" {
		w.state.LastOutput += "\n" + msg
	} else {
		w.state.LastOutput = msg
	}
	w.mu.Unlock()
	if w.terminal != nil && w.terminal.IsTerminal() {
		w.renderFullView()
	}
}

// updateTmuxStatusLine updates the tmux status bar with the states of all active agents.
func (w *Workspace) updateTmuxStatusLine(ctx context.Context) {
	if !w.isTmuxActive() {
		return
	}
	w.tmuxMu.Lock()
	var parts []string
	for _, agent := range w.tmuxActiveWins {
		if agent.role == "marshal-chat" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", agent.label, agent.state))
	}
	session := w.tmuxSession
	w.tmuxMu.Unlock()

	statusText := ""
	if len(parts) > 0 {
		statusText = fmt.Sprintf(" [%s] ", strings.Join(parts, " | "))
	}
	_ = tmux.SetStatusText(ctx, session, statusText)
}

// reportActiveTmuxSessions prints still-running agent sessions when MARSHAL exits.
func (w *Workspace) reportActiveTmuxSessions() {
	if !w.isTmuxActive() {
		return
	}
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	if len(w.tmuxActiveWins) == 0 {
		return
	}
	fmt.Fprintln(w.out, "\nActive native agent sessions still running in tmux:")
	for _, agent := range w.tmuxActiveWins {
		fmt.Fprintf(w.out, "  • %s (window: %s)\n", agent.label, agent.window)
	}
	fmt.Fprintf(w.out, "Re-attach to this layout anytime with: tmux attach-session -t %s\n", w.tmuxSession)
	fmt.Fprintln(w.out, "Ending an agent session remains an explicit action.")
}

// StopAllWorkers terminates all worker agent windows and process groups while keeping MARSHAL intact.
// Decision 9: One key stops all workers but never the Marshal (and never the Marshal planning chat).
func (w *Workspace) StopAllWorkers(ctx context.Context) string {
	if !w.isTmuxActive() {
		return "No active tmux session."
	}
	w.tmuxMu.Lock()
	if len(w.tmuxActiveWins) == 0 {
		w.tmuxMu.Unlock()
		return "No active worker sessions to stop."
	}

	var targets []*activeTmuxAgent
	for _, agent := range w.tmuxActiveWins {
		if agent.role != "marshal-chat" {
			targets = append(targets, agent)
		}
	}
	w.tmuxMu.Unlock()

	var stopped []string
	root := w.workDir
	if w.runtime != nil && w.runtime.ProjectRoot() != "" {
		root = w.runtime.ProjectRoot()
	}

	for _, agent := range targets {
		if agent.cancel != nil {
			agent.cancel()
		}

		// 1. Capture screen output as evidence BEFORE killing (Decision 8)
		evidence, _ := tmux.CapturePane(ctx, agent.paneID)
		if evidence == "" {
			evidence, _ = tmux.CapturePane(ctx, agent.window)
		}
		if evidence != "" {
			_ = saveAgentEvidence(root, agent.id, evidence)
		}

		// 2. Kill the process group cleanly
		tmux.KillProcessGroup(agent.pid, agent.pgid)

		// 3. Kill the pane or window
		_ = tmux.KillPane(ctx, agent.paneID)
		_ = tmux.KillWindow(ctx, agent.window)

		if agent.role == "worker" {
			if fkey := providerFKey(agent.provider); fkey != "" {
				_ = tmux.UnbindGlobalKey(ctx, fkey)
			}
		}
		if agent.briefingDir != nil {
			agent.briefingDir.remove()
		}
		stopped = append(stopped, agent.label)

		w.tmuxMu.Lock()
		delete(w.tmuxActiveWins, agent.id)
		w.tmuxMu.Unlock()
	}

	// Update status line
	w.updateTmuxStatusLine(ctx)

	if len(stopped) == 0 {
		return "No active worker sessions to stop. MARSHAL remains active."
	}
	return fmt.Sprintf("Stopped all worker sessions (%s). MARSHAL remains active.", strings.Join(stopped, ", "))
}

func (w *Workspace) activeWorkerAgentLocked() *activeTmuxAgent {
	if w.nativeProvider != "" {
		if a, ok := w.tmuxActiveWins[w.nativeProvider]; ok && a.role != "marshal-chat" {
			return a
		}
	}
	for _, a := range w.tmuxActiveWins {
		if a.role != "marshal-chat" {
			return a
		}
	}
	return nil
}

// handleViewCommand implements the /view command family.
func (w *Workspace) handleViewCommand(ctx context.Context, args []string) (string, error) {
	if !w.isTmuxActive() {
		return "View commands require MARSHAL to run inside tmux.", nil
	}
	if len(args) == 0 {
		return "Usage: /view [focus | side-by-side | worker | show <agent> | hide | follow | readonly | takeover]", nil
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "focus", "marshal":
		// Break any joined panes back out into their windows so only Marshal is visible
		w.tmuxMu.Lock()
		for _, a := range w.tmuxActiveWins {
			if a.isJoined {
				_ = tmux.BreakPane(ctx, a.paneID, a.window)
				a.isJoined = false
			}
		}
		mWin := w.tmuxMarshalWin
		mPane := w.tmuxMarshalPaneID
		w.tmuxMu.Unlock()

		_ = tmux.SelectWindow(ctx, mWin)
		if mPane != "" {
			_ = tmux.SelectPane(ctx, mPane)
		}
		return "View: showing only MARSHAL (focus).", nil

	case "side-by-side", "split":
		w.tmuxMu.Lock()
		active := w.activeWorkerAgentLocked()
		w.tmuxMu.Unlock()
		if active == nil {
			return "No active worker session to show side-by-side.", nil
		}
		target := w.tmuxSession + ":" + w.tmuxMarshalWin
		if err := tmux.JoinPane(ctx, active.paneID, target, true); err != nil {
			return "", fmt.Errorf("show side-by-side: %w", err)
		}
		w.tmuxMu.Lock()
		active.isJoined = true
		w.tmuxMu.Unlock()
		return fmt.Sprintf("View: showing MARSHAL and %s side-by-side.", active.label), nil

	case "worker", "active":
		w.tmuxMu.Lock()
		active := w.activeWorkerAgentLocked()
		w.tmuxMu.Unlock()
		if active == nil {
			return "No active worker session.", nil
		}
		if active.isJoined {
			if err := tmux.SelectPane(ctx, active.paneID); err != nil {
				return "", err
			}
		} else {
			if err := tmux.SelectWindow(ctx, active.window); err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("View: showing active worker %s. Press F11 to return to MARSHAL.", active.label), nil

	case "show":
		if len(args) < 2 {
			return "Usage: /view show <agent>  (e.g. codex, claude, opencode, agy, chat, marshal)", nil
		}
		targetAgent := strings.ToLower(args[1])
		switch targetAgent {
		case "marshal":
			_ = tmux.SelectWindow(ctx, w.tmuxMarshalWin)
			return "View: showing MARSHAL.", nil
		case "chat":
			w.tmuxMu.Lock()
			chatAgent, exists := w.tmuxActiveWins["marshal-chat"]
			w.tmuxMu.Unlock()
			if exists {
				_ = tmux.SelectWindow(ctx, chatAgent.window)
				return "View: showing Marshal Chat. Press F11 to return to MARSHAL.", nil
			}
			return "No active Marshal Chat session.", nil
		case "codex", "claude", "opencode", "agy", "antigravity":
			norm := targetAgent
			if norm == "agy" {
				norm = "antigravity"
			}
			w.tmuxMu.Lock()
			agent, exists := w.tmuxActiveWins[norm]
			if exists {
				w.nativeProvider = agent.provider
			}
			w.tmuxMu.Unlock()
			if exists {
				if agent.isJoined {
					_ = tmux.SelectPane(ctx, agent.paneID)
				} else {
					_ = tmux.SelectWindow(ctx, agent.window)
				}
				return fmt.Sprintf("View: showing %s. Press F11 to return to MARSHAL.", agent.label), nil
			}
			cmd := "/" + targetAgent
			resp, err := w.cmd.Handle(ctx, cmd)
			if err != nil {
				return "", err
			}
			return resp, nil
		default:
			return fmt.Sprintf("Unknown agent %q. Available: codex, claude, opencode, agy, chat, marshal", targetAgent), nil
		}

	case "hide", "others":
		w.tmuxMu.Lock()
		for _, a := range w.tmuxActiveWins {
			if a.isJoined {
				_ = tmux.BreakPane(ctx, a.paneID, a.window)
				a.isJoined = false
			}
		}
		mWin := w.tmuxMarshalWin
		w.tmuxMu.Unlock()
		_ = tmux.SelectWindow(ctx, mWin)
		return "View: hidden other panes, MARSHAL focused.", nil

	case "follow", "follow-active":
		w.tmuxMu.Lock()
		w.tmuxFollowActive = !w.tmuxFollowActive
		state := w.tmuxFollowActive
		w.tmuxMu.Unlock()
		return fmt.Sprintf("View: follow-active is now %v.", state), nil

	case "readonly", "view-only":
		w.tmuxMu.Lock()
		active := w.activeWorkerAgentLocked()
		w.tmuxMu.Unlock()
		if active == nil {
			return "No active worker session.", nil
		}
		if err := tmux.SetPaneReadOnly(ctx, active.paneID, true); err != nil {
			return "", err
		}
		active.readOnly = true
		return fmt.Sprintf("View: %s pane set to view-only.", active.label), nil

	case "takeover":
		return w.handleTakeoverCommand(ctx)

	default:
		return "Usage: /view [focus | side-by-side | worker | show <agent> | hide | follow | readonly | takeover]", nil
	}
}

// handleTakeoverCommand enables input in the active worker pane.
func (w *Workspace) handleTakeoverCommand(ctx context.Context) (string, error) {
	if !w.isTmuxActive() {
		return "Takeover requires MARSHAL to run inside tmux.", nil
	}
	w.tmuxMu.Lock()
	active := w.activeWorkerAgentLocked()
	w.tmuxMu.Unlock()
	if active == nil {
		return "No active worker session to take over.", nil
	}
	if err := tmux.SetPaneReadOnly(ctx, active.paneID, false); err != nil {
		return "", err
	}
	if active.isJoined {
		_ = tmux.SelectPane(ctx, active.paneID)
	} else {
		_ = tmux.SelectWindow(ctx, active.window)
	}
	active.readOnly = false
	return fmt.Sprintf("Takeover: input enabled for %s. You can now type directly into the session. Press F11 to return to MARSHAL.", active.label), nil
}

func saveAgentEvidence(root, identifier, evidence string) error {
	dir := filepath.Join(root, ".marshal", "evidence")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create evidence directory: %w", err)
	}
	timestamp := time.Now().UTC().Format("20060102-150405")
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.txt", identifier, timestamp))
	if err := os.WriteFile(path, []byte(evidence), 0o600); err != nil {
		return fmt.Errorf("write evidence file: %w", err)
	}
	latest := filepath.Join(dir, fmt.Sprintf("%s-latest.txt", identifier))
	if err := os.WriteFile(latest, []byte(evidence), 0o600); err != nil {
		return fmt.Errorf("write latest evidence file: %w", err)
	}
	return nil
}

// watchMarshalDraft starts background monitoring for draft completion when Marshal Chat is active.
func (w *Workspace) watchMarshalDraft(m *marshalSession, runID, root, provider, note string) {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-time.After(30 * time.Minute):
				return
			case <-ticker.C:
				data, exists, err := consumeMarshalDraft(root)
				if err != nil || !exists {
					w.tmuxMu.Lock()
					_, chatAlive := w.tmuxActiveWins["marshal-chat"]
					w.tmuxMu.Unlock()
					if !chatAlive {
						return
					}
					continue
				}
				service := m.service
				if service == nil {
					return
				}
				packDir, packErr := service.TakePlanPack(runID)
				draft, err := service.DraftFromProposal(data, provider)
				if err != nil || packErr != nil {
					return
				}
				ids := make([]string, 0, len(draft.Tasks))
				for _, task := range draft.Tasks {
					ids = append(ids, task.PlanTaskID)
				}
				pack, err := app.ReadPlanPack(packDir, ids)
				if err != nil {
					return
				}
				draft.Pack = &pack
				goal := draft.Plan.Goal.GoalID
				if goal == "" {
					goal = draft.Plan.ID
				}
				run, err := service.StartPlanningFromDraft(context.Background(), runID, goal, draft, marshal.Budget{})
				if err != nil {
					return
				}
				m.mu.Lock()
				m.runID, m.service, m.provider, m.amended, m.pending = runID, service, provider, false, nil
				m.approvals = nil
				m.mu.Unlock()
				w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, fmt.Sprintf("plan drafted: %d tasks · read %s · /marshal approve to run it", len(run.Tasks), packDir)))
				w.RecordActivity(fmt.Sprintf("Marshal plan drafted (%d tasks). Read the plan in %s, then use /marshal approve in MARSHAL to run it.", len(run.Tasks), packDir))
				return
			}
		}
	}()
}

// wrapServiceDriversForTmux wraps the drivers in MarshalService to integrate dispatched task workers with tmux.
func (w *Workspace) wrapServiceDriversForTmux(service *app.MarshalService) {
	if service == nil {
		return
	}
	for name, d := range service.Drivers {
		service.Drivers[name] = &tmuxTaskDriver{inner: d, w: w, name: name}
	}
	for name, d := range service.GovernedDrivers {
		service.GovernedDrivers[name] = &tmuxTaskDriver{inner: d, w: w, name: name}
	}
}

type tmuxTaskDriver struct {
	inner driver.Driver
	w     *Workspace
	name  string
}

func (t *tmuxTaskDriver) Mode() marshal.WorkerMode {
	return t.inner.Mode()
}

func (t *tmuxTaskDriver) Launch(ctx context.Context, req driver.Request) (*driver.Handle, error) {
	taskID := req.Task.PlanTaskID
	worker := req.Task.Worker
	root := t.w.workDir
	if t.w.runtime != nil && t.w.runtime.ProjectRoot() != "" {
		root = t.w.runtime.ProjectRoot()
	}
	winName := tmux.TaskWindowName(taskID, root)

	_ = tmux.NewWindow(ctx, t.w.tmuxSession, winName, req.Worktree, nil, []string{"sleep", "3600"})
	_ = tmux.SetWindowOption(ctx, winName, "remain-on-exit", "on")
	_ = tmux.SetPaneReadOnly(ctx, winName, true)

	paneID := winName
	panes, _ := tmux.ListPanes(ctx, t.w.tmuxSession)
	for _, p := range panes {
		if p.WindowName == winName {
			paneID = p.PaneID
			break
		}
	}

	taskCtx, cancel := context.WithCancel(ctx)
	h, err := t.inner.Launch(taskCtx, req)
	if err != nil {
		cancel()
		_ = tmux.KillWindow(ctx, winName)
		return nil, err
	}

	pid, pgid, _ := tmux.PanePIDAndPGID(ctx, paneID)

	agentID := "task-" + taskID
	agent := &activeTmuxAgent{
		id:       agentID,
		role:     "task",
		taskID:   taskID,
		provider: worker,
		label:    fmt.Sprintf("Task %s (%s)", taskID, worker),
		window:   winName,
		paneID:   paneID,
		pid:      pid,
		pgid:     pgid,
		state:    "working",
		readOnly: true,
		cancel:   cancel,
		doneChan: make(chan struct{}),
	}

	t.w.tmuxMu.Lock()
	t.w.tmuxActiveWins[agentID] = agent
	t.w.tmuxMu.Unlock()
	t.w.updateTmuxStatusLine(ctx)
	t.w.RecordActivity(fmt.Sprintf("Dispatched task %s to %s (tmux window %s).", taskID, worker, winName))

	return h, nil
}

func (t *tmuxTaskDriver) Wait(ctx context.Context, h *driver.Handle) (marshal.HandIn, error) {
	handin, err := t.inner.Wait(ctx, h)

	taskID := h.Request().Task.PlanTaskID
	agentID := "task-" + taskID
	root := t.w.workDir
	if t.w.runtime != nil && t.w.runtime.ProjectRoot() != "" {
		root = t.w.runtime.ProjectRoot()
	}

	t.w.tmuxMu.Lock()
	agent, ok := t.w.tmuxActiveWins[agentID]
	t.w.tmuxMu.Unlock()

	if ok && agent != nil {
		evidence, _ := tmux.CapturePane(context.Background(), agent.paneID)
		if evidence != "" {
			_ = saveAgentEvidence(root, agentID, evidence)
		}
		tmux.KillProcessGroup(agent.pid, agent.pgid)
		_ = tmux.KillPane(context.Background(), agent.paneID)
		_ = tmux.KillWindow(context.Background(), agent.window)

		t.w.tmuxMu.Lock()
		delete(t.w.tmuxActiveWins, agentID)
		t.w.tmuxMu.Unlock()
		t.w.updateTmuxStatusLine(context.Background())
		t.w.RecordActivity(fmt.Sprintf("Task %s completed.", taskID))
	}

	return handin, err
}

func (t *tmuxTaskDriver) Cancel(h *driver.Handle) error {
	taskID := h.Request().Task.PlanTaskID
	agentID := "task-" + taskID
	root := t.w.workDir
	if t.w.runtime != nil && t.w.runtime.ProjectRoot() != "" {
		root = t.w.runtime.ProjectRoot()
	}

	t.w.tmuxMu.Lock()
	agent, ok := t.w.tmuxActiveWins[agentID]
	delete(t.w.tmuxActiveWins, agentID)
	t.w.tmuxMu.Unlock()

	if ok && agent != nil {
		evidence, _ := tmux.CapturePane(context.Background(), agent.paneID)
		if evidence != "" {
			_ = saveAgentEvidence(root, agentID, evidence)
		}
		tmux.KillProcessGroup(agent.pid, agent.pgid)
		_ = tmux.KillPane(context.Background(), agent.paneID)
		_ = tmux.KillWindow(context.Background(), agent.window)
		t.w.updateTmuxStatusLine(context.Background())
	}

	return t.inner.Cancel(h)
}
