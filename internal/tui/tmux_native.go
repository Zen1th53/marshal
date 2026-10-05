package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/workerterminal"
)

type activeTmuxAgent struct {
	canonicalTaskID string
	executionRunID  string
	id              string // e.g. "codex", "claude", "marshal-chat", "task-01"
	role            string // "worker", "marshal-chat", "task"
	provider        string
	taskID          string
	label           string
	window          string
	windowID        string
	paneID          string
	pid             int
	pgid            int
	state           string // "working", "waiting", "done", "failed"
	readOnly        bool
	isJoined        bool
	cancel          context.CancelFunc
	briefingDir     *briefingDir
	doneChan        chan struct{}
	binary          string
	args            []string
	env             []string
	historyBaseline []string
	sessionID       string
	runID           string
	driver          driver.Driver
	handle          *driver.Handle
	supervisor      processgroup.Reference
	cleanupMu       sync.Mutex
	cleaned         bool
}

// InitTmux initializes tmux session and window awareness for the workspace.
func (w *Workspace) InitTmux(root ...string) {
	w.tmuxMu.Lock()
	defer func() {
		w.tmuxMu.Unlock()
		if w.runtime != nil && tmux.IsInsideTmux() {
			if _, err := w.marshalChat(context.Background()); err != nil {
				w.RecordActivity("Marshal chat: " + err.Error())
			}
		}
	}()

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

		_ = w.bindWorkspaceKeysLocked(context.Background(), w.tmuxMarshalPaneID, projectRoot)
	} else {
		w.tmuxSession = sessionName
		w.tmuxMarshalWin = "marshal"
	}

	// Adopt surviving windows from existing session if re-attaching
	w.adoptSurvivingWorkersLocked(projectRoot)

	// Ensure Marshal Chat is automatically open (F3)
	if w.runtime == nil {
		w.ensureMarshalChatAutoLocked(projectRoot)
	}
}

func (w *Workspace) adoptSurvivingWorkersLocked(projectRoot string) {
	if w.tmuxSession == "" {
		return
	}
	panes, err := tmux.ListPanes(context.Background(), w.tmuxSession)
	if err != nil {
		return
	}
	for _, p := range panes {
		record, err := loadAgentRecord(projectRoot, p.PaneID)
		if err != nil || record.Project != tmux.ProjectHash(projectRoot) || record.Session != w.tmuxSession {
			continue
		}
		if _, exists := w.tmuxActiveWins[record.ID]; exists {
			continue
		}
		agent := &activeTmuxAgent{id: record.ID, role: record.Role, taskID: record.TaskID, runID: record.RunID, executionRunID: record.ExecutionRunID, canonicalTaskID: record.CanonicalTaskID, provider: record.Provider, label: record.Label, window: record.Window, windowID: p.WindowID, paneID: p.PaneID, state: "working", readOnly: record.Role != "marshal-chat", supervisor: record.Supervisor, isJoined: p.WindowID == w.tmuxMarshalWinID}
		agentCtx, cancel := context.WithCancel(context.Background())
		agent.cancel = cancel
		agent.doneChan = make(chan struct{})
		var watch *nativeHistoryWatch
		if record.Role == "marshal-chat" {
			saved := loadChatBinding(projectRoot)
			agent.binary = saved.Binary
			agent.args = saved.Args
			agent.env = saved.Env
			agent.sessionID = saved.SessionID
			agent.historyBaseline = saved.HistoryBaseline
			watch = w.chatHistoryWatch(projectRoot, agent.provider, agent.binary)
			baseline, err := w.prepareChatHistoryWatch(projectRoot, watch, saved.SessionID, saved.HistoryBaseline)
			if err == nil {
				agent.historyBaseline = baseline
				err = w.saveChatBindingLocked(projectRoot, agent)
			}
			if err != nil {
				cancel()
				w.RecordActivity(err.Error())
				continue
			}
		}
		w.tmuxActiveWins[record.ID] = agent
		w.monitorAgent(agentCtx, agent, projectRoot, nil, watch, nil, nil, nil)
	}
}

// ensureMarshalChatAutoLocked opens the Marshal chat pane automatically if not already active (F3).
func (w *Workspace) ensureMarshalChatAutoLocked(projectRoot string) {
	if w.tmuxSession == "" {
		return
	}
	if _, ok := w.tmuxActiveWins["marshal-chat"]; ok {
		return
	}
	chatWin := tmux.ChatWindowName(projectRoot)
	ctx := context.Background()
	exists, _ := tmux.WindowExists(ctx, w.tmuxSession, chatWin)
	if exists {
		w.adoptSurvivingWorkersLocked(projectRoot)
		if _, ok := w.tmuxActiveWins["marshal-chat"]; ok {
			return
		}
	}
	w.startMarshalChatLocked(ctx, projectRoot)
}

func (w *Workspace) startMarshalChatLocked(ctx context.Context, projectRoot string) {
	provider := loadDefaultProvider(w.providerRoot())
	if provider == "" {
		provider = "codex"
	}
	binaryName := provider
	if provider == "antigravity" {
		binaryName = "agy"
	}
	binary, err := exec.LookPath(binaryName)
	if err != nil {
		binary = binaryName
	}

	winName := tmux.ChatWindowName(projectRoot)
	brief, err := marshalRoleBriefing(app.MarshalWorkers(provider), marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		return
	}
	var args []string
	var env []string
	var dir *briefingDir
	if hiddenChannel(provider) == injectMarshalDir {
		dir, err = newBriefingDir(projectRoot, provider)
		if err == nil {
			_, err = dir.add(brief)
		}
		if err == nil {
			args, env, err = dir.launch(marshalKickoffArgs(provider))
		}
	} else {
		args, _, err = applyBriefing(provider, projectRoot, marshalKickoffArgs(provider), brief, hiddenChannel(provider))
	}
	if err != nil {
		return
	}
	saved := loadChatBinding(projectRoot)
	runID := saved.RunID
	m := w.marshalSession()
	m.mu.Lock()
	if m.runID != "" {
		runID = m.runID
	}
	m.mu.Unlock()
	if saved.Provider == provider && saved.SessionID != "" {
		args = append(resumeArgsForProvider(provider, saved.SessionID), args...)
	}
	for _, key := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "HOME"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	watch := w.chatHistoryWatch(projectRoot, provider, binary)
	baseline, err := w.prepareChatHistoryWatch(projectRoot, watch, saved.SessionID)
	if err != nil {
		return
	}
	// Persist the prelaunch history even if the process crashes before binding.
	if err := saveChatBinding(projectRoot, chatBinding{Provider: provider, Binary: binary, Args: args, Env: env, SessionID: saved.SessionID, RunID: runID, HistoryBaseline: baseline}); err != nil {
		return
	}
	cmd := append([]string{binary}, args...)

	_ = tmux.NewWindow(ctx, w.tmuxSession, winName, projectRoot, env, cmd)
	_ = tmux.SetWindowOption(ctx, w.tmuxSession+":"+winName, "remain-on-exit", "on")
	_ = tmux.SetPaneReadOnly(ctx, w.tmuxSession+":"+winName, false) // F3: normal operator input

	// Keep the main MARSHAL workspace window focused
	if w.tmuxMarshalWin != "" {
		_ = tmux.SelectWindow(ctx, w.marshalTarget())
	}

	paneID := winName
	windowID := ""
	pid := 0
	pgid := 0

	panes, _ := tmux.ListPanes(ctx, w.tmuxSession)
	for _, p := range panes {
		if p.WindowName == winName {
			paneID = p.PaneID
			windowID = p.WindowID
			pidVal, pgidVal, err := tmux.PanePIDAndPGID(ctx, paneID)
			if err == nil && pidVal > 0 {
				pid = pidVal
				pgid = pgidVal
			} else {
				pid = p.PID
				pgid = p.PID
			}
			break
		}
	}

	agentCtx, cancel := context.WithCancel(context.Background())
	agent := &activeTmuxAgent{
		id:              "marshal-chat",
		role:            "marshal-chat",
		provider:        provider,
		label:           "Marshal Chat",
		window:          winName,
		windowID:        windowID,
		paneID:          paneID,
		pid:             pid,
		pgid:            pgid,
		state:           "working",
		readOnly:        false,
		cancel:          cancel,
		doneChan:        make(chan struct{}),
		binary:          binary,
		args:            args,
		env:             env,
		briefingDir:     dir,
		sessionID:       saved.SessionID,
		historyBaseline: baseline,
		runID:           runID,
	}

	w.tmuxActiveWins["marshal-chat"] = agent
	_ = w.bindWorkspaceKeysLocked(ctx, agent.paneID, projectRoot)
	_ = w.saveChatBindingLocked(projectRoot, agent)
	w.monitorAgent(agentCtx, agent, projectRoot, dir, watch, nil, nil, nil)
}

func (w *Workspace) restartMarshalChat(ctx context.Context, agent *activeTmuxAgent, root string) {
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()

	provider := agent.provider
	if provider == "" {
		provider = loadDefaultProvider(w.providerRoot())
	}
	if provider == "" {
		provider = "codex"
	}

	sessionID := agent.sessionID
	if sessionID == "" {
		if m := w.marshalSession(); m != nil {
			m.mu.Lock()
			sessionID = m.conversationID
			m.mu.Unlock()
		}
	}

	bin := agent.binary
	if bin == "" {
		binaryName := provider
		if provider == "antigravity" {
			binaryName = "agy"
		}
		if resolved, err := exec.LookPath(binaryName); err == nil {
			bin = resolved
		} else {
			bin = binaryName
		}
		agent.binary = bin
	}

	resumeArgs := agent.args
	if sessionID != "" {
		resumeArgs = resumeArgsForProvider(provider, sessionID)
	}
	agent.args = resumeArgs
	cmd := append([]string{bin}, resumeArgs...)

	err := tmux.RespawnWindow(ctx, agent.paneID, cmd)
	if err != nil {
		_ = tmux.NewWindow(ctx, w.tmuxSession, agent.window, root, agent.env, cmd)
	}

	_ = tmux.SetWindowOption(ctx, agent.paneID, "remain-on-exit", "on")
	_ = tmux.SetPaneReadOnly(ctx, agent.paneID, false) // F3: normal operator input

	panes, _ := tmux.ListPanes(ctx, w.tmuxSession)
	for _, p := range panes {
		if p.WindowName == agent.window {
			agent.paneID = p.PaneID
			agent.windowID = p.WindowID
			pidVal, pgidVal, err := tmux.PanePIDAndPGID(ctx, p.PaneID)
			if err == nil && pidVal > 0 {
				agent.pid = pidVal
				agent.pgid = pgidVal
			} else {
				agent.pid = p.PID
				agent.pgid = p.PID
			}
			break
		}
	}

	agent.state = "working"
	agent.readOnly = false
	_ = w.bindWorkspaceKeysLocked(ctx, agent.paneID, root)
	_ = w.saveChatBindingLocked(root, agent)
}

func resumeArgsForProvider(provider, sessionID string) []string {
	switch provider {
	case "codex":
		if sessionID != "" {
			return []string{"resume", sessionID}
		}
		return nil
	case "claude":
		if sessionID != "" {
			return []string{"--resume", sessionID}
		}
		return nil
	case "opencode":
		if sessionID != "" {
			return []string{"session", "resume", sessionID}
		}
		return nil
	case "antigravity", "agy":
		if sessionID != "" {
			return []string{"--conversation", sessionID}
		}
		return []string{"resume"}
	default:
		if sessionID != "" {
			return []string{"resume", sessionID}
		}
		return []string{"resume"}
	}
}

// isTmuxActive reports whether tmux is available and MARSHAL is connected to it.
func (w *Workspace) isTmuxActive() bool {
	if !tmux.IsInsideTmux() && os.Getenv("MARSHAL_TEST_FORCE_TMUX") != "1" {
		return false
	}
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
	existingAgent = copyAgentLocked(existingAgent)
	w.tmuxMu.Unlock()

	winExists := false
	if existingAgent != nil {
		panes, err := tmux.ListPanes(ctx, w.tmuxSession)
		if err != nil {
			return "", err
		}
		for _, p := range panes {
			if p.PaneID == existingAgent.paneID {
				winExists = true
				break
			}
		}
	}
	if exists && winExists {
		// Window already exists; switch to it without restarting the process.
		if existingAgent != nil && existingAgent.isJoined {
			_ = tmux.SelectPane(ctx, existingAgent.paneID)
		} else {
			_ = tmux.SelectWindow(ctx, existingAgent.paneID)
		}
		if !isChat {
			w.tmuxMu.Lock()
			w.nativeProvider = provider
			w.tmuxMu.Unlock()
		}
		w.updateTmuxStatusLine(ctx)
		return fmt.Sprintf("Switched to active %s session (tmux window %s). The session continues running. Press F11 to return to MARSHAL.", agentLabel, winName), nil
	}

	// Resume only the conversation bound to this project.
	if isChat {
		saved := loadChatBinding(root)
		if saved.Provider == provider && saved.SessionID != "" {
			args = append(resumeArgsForProvider(provider, saved.SessionID), args...)
		}
	}
	// The driver owns native worker execution; tmux hosts its terminal relay.
	var workerHandle *driver.Handle
	var sessionReference processgroup.Reference
	if isChat {
		if err := tmux.NewWindow(ctx, w.tmuxSession, winName, root, briefingEnv, append([]string{binary}, args...)); err != nil {
			return "", err
		}
	} else {
		host := func(ctx context.Context, socket string) error {
			relay, err := exec.LookPath("socat")
			if err != nil {
				return err
			}
			target := w.tmuxSession + ":" + winName
			if err := tmux.NewWindow(ctx, w.tmuxSession, winName, root, nil, []string{relay, "STDIO,raw,echo=0", "UNIX-CONNECT:" + socket}); err != nil {
				return err
			}
			if err := tmux.SetWindowOption(ctx, target, "remain-on-exit", "on"); err != nil {
				_ = tmux.KillPane(context.Background(), target)
				return err
			}
			if err := tmux.SetPaneReadOnly(ctx, target, true); err != nil {
				_ = tmux.KillPane(context.Background(), target)
				return err
			}
			return nil
		}
		if err := resetAgentOutcome(root, agentID); err != nil {
			return "", err
		}
		var err error
		sessionCtx := workerterminal.WithLifecycle(workerterminal.WithCompletion(workerterminal.WithHost(context.Background(), host), agentCompletion(root, agentID)), func(ref processgroup.Reference) error { sessionReference = ref; return nil })
		workerHandle, err = driver.LaunchSession(sessionCtx, provider, binary, root, args, briefingEnv)
		if err != nil {
			return "", fmt.Errorf("launch %s in tmux: %w", agentLabel, err)
		}
	}

	// Hosting guarantees must hold before publishing a managed terminal.
	failHost := func(err error) (string, error) {
		if workerHandle != nil {
			_ = (driver.Native{}).Cancel(workerHandle)
		}
		_ = tmux.KillPane(context.Background(), w.tmuxSession+":"+winName)
		return "", err
	}
	if err := tmux.SetWindowOption(ctx, w.tmuxSession+":"+winName, "remain-on-exit", "on"); err != nil {
		return failHost(err)
	}
	if err := tmux.SetPaneReadOnly(ctx, w.tmuxSession+":"+winName, !isChat); err != nil {
		return failHost(err)
	}

	// Switch to the newly opened window
	_ = tmux.SelectWindow(ctx, w.tmuxSession+":"+winName)

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
				} else {
					pgid = pid
				}
			}
			break
		}
	}

	// Bind provider's F-key and F11 in tmux (for workers only, not chat)
	if !isChat {
		fkey := providerFKey(provider)
		if fkey != "" {
			_ = tmux.BindWindowKey(ctx, paneID, "marshal-keys-"+tmux.ProjectHash(root), fkey, "select-window", "-t", paneID)
		}
	}
	w.tmuxMu.Lock()
	_ = w.bindWorkspaceKeysLocked(ctx, paneID, root)
	w.tmuxMu.Unlock()

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
		readOnly:    !isChat,
		cancel:      cancel,
		briefingDir: dir,
		doneChan:    make(chan struct{}),
		binary:      binary,
		args:        args,
		env:         briefingEnv,
		handle:      workerHandle,
		supervisor:  sessionReference,
		driver:      driver.Native{},
	}

	w.tmuxMu.Lock()
	w.tmuxActiveWins[agentID] = agent
	if err := saveAgentRecord(root, w.tmuxSession, agent); err != nil {
		w.tmuxMu.Unlock()
		if workerHandle != nil {
			_ = agent.driver.Cancel(workerHandle)
		}
		return "", err
	}
	if isChat {
		saved := loadChatBinding(root)
		agent.sessionID = saved.SessionID
		agent.historyBaseline = saved.HistoryBaseline
		_ = w.saveChatBindingLocked(root, agent)
	}
	if !isChat {
		w.nativeProvider = provider
	}
	w.tmuxMu.Unlock()
	w.updateTmuxStatusLine(ctx)

	w.monitorAgent(agentCtx, agent, root, dir, watch, peers, chStream, view)

	msg := fmt.Sprintf("Opened native %s in tmux window %s (view-only mode). Press F11 to return to MARSHAL. Use /takeover to enable typing.", agentLabel, winName)
	if isChat {
		msg = fmt.Sprintf("Opened native %s in tmux window %s. Press F11 to return to MARSHAL.", agentLabel, winName)
	}
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
	w.tmuxMonitors.Add(1)
	go func() {
		defer w.tmuxMonitors.Done()
		defer close(agent.doneChan)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-agentCtx.Done():
				return
			case <-ticker.C:
				w.tmuxMu.Lock()
				snapshot := copyAgentLocked(agent)
				w.tmuxMu.Unlock()
				// Poll syncs
				if watch != nil && capturesLive(snapshot.provider) {
					_ = watch.sync()
				}
				for _, pw := range peers {
					_ = pw.sync()
				}
				if chStream != nil && view != nil {
					_ = refreshInboxView(root, view, chStream)
				}
				if view != nil {
					status := liveStatus{Delivered: view.Count(), LastSync: time.Now().UTC()}
					_ = writeLiveStatus(root, snapshot.provider, status)
				}

				// Check whether pane is dead
				dead, exitCode, err := tmux.PaneDeadStatus(context.Background(), snapshot.paneID)
				if err != nil {
					// Fallback to checking window existence if pane query failed
					exists, _ := tmux.WindowExists(context.Background(), w.tmuxSession, snapshot.window)
					if !exists {
						dead = true
					}
				}

				if snapshot.handle != nil {
					select {
					case <-snapshot.handle.Done():
						dead = true
						record, _, _ := snapshot.handle.Outcome()
						exitCode = record.ExitCode
					default:
						dead = false
					}
				}
				if snapshot.handle == nil && snapshot.role != "marshal-chat" {
					if data, readErr := os.ReadFile(agentOutcomePath(root, snapshot.id)); readErr == nil && json.Unmarshal(data, &exitCode) == nil {
						dead = true
					} else if snapshot.supervisor.PID > 0 {
						// A relay exit cannot certify worker success. If the pinned
						// supervisor vanished without a result, retain an unknown failure.
						alive, identityErr := processgroup.RunningReference(snapshot.supervisor)
						// Completion publishes the outcome before closing the relay.
						// A reaped supervisor alone does not mean publication failed.
						dead = dead && identityErr == nil && !alive
						exitCode = -1
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

					if snapshot.role == "marshal-chat" {
						// F3: The Marshal chat is never killed by the worker monitor.
						// If it exits unexpectedly, it is restarted automatically and resumes
						// the stored run / conversation.
						if agentCtx.Err() == nil {
							w.RecordActivity(fmt.Sprintf("%s exited unexpectedly; restarting and resuming conversation...", snapshot.label))
							w.restartMarshalChat(context.Background(), agent, root)
							w.updateTmuxStatusLine(context.Background())
							continue
						}
					}

					state := "done"
					if exitCode != 0 {
						state = "failed"
					}
					if err := w.deliverEgressAlert(app.EgressAlert{RunID: snapshot.runID, ParentRunID: snapshot.runID, TaskID: snapshot.taskID, Worker: snapshot.provider, Kind: "worker " + state, State: state, Message: snapshot.label + " worker " + state + "."}); err != nil {
						w.RecordActivity(err.Error())
					}
					if err := w.retainAndCloseAgent(context.Background(), agent, root, state); err != nil {
						w.RecordActivity(err.Error())
						continue
					}
					if dir != nil {
						dir.remove()
					}

					w.RecordActivity(fmt.Sprintf("%s session ended (exit %d). Output saved to evidence.", snapshot.label, exitCode))
					w.updateTmuxStatusLine(context.Background())
					return
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
	// Serialize before taking the snapshot: an older projection must not
	// finish after a newer one. Status reads never initialize another chat.
	w.tmuxStatusMu.Lock()
	defer w.tmuxStatusMu.Unlock()
	w.tmuxMu.Lock()
	if w.tmuxPath == "" || w.tmuxSession == "" {
		w.tmuxMu.Unlock()
		return
	}
	var parts []string
	for _, agent := range w.tmuxActiveWins {
		if agent.role == "marshal-chat" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", agent.label, agent.state))
	}
	for _, state := range w.tmuxAlerts {
		parts = append(parts, state)
	}
	sort.Strings(parts)
	target := w.marshalTarget()
	w.tmuxMu.Unlock()

	statusText := ""
	if len(parts) > 0 {
		statusText = fmt.Sprintf(" [%s] ", strings.Join(parts, " | "))
	}
	_ = tmux.SetWindowStatus(ctx, target, statusText)
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
	w.cancelGovernedDispatches()
	w.governedDispatchWG.Wait()
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

	var failures []string
	for _, agent := range targets {
		if err := w.stopAgent(agent); err != nil {
			failures = append(failures, agent.label+": "+err.Error())
			continue
		}
		if err := w.retainAndCloseAgent(ctx, agent, root, "stopped by operator"); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if agent.cancel != nil {
			agent.cancel()
		}
		if agent.doneChan != nil {
			<-agent.doneChan
		}
		if agent.briefingDir != nil {
			agent.briefingDir.remove()
		}
		stopped = append(stopped, agent.label)
	}
	if len(failures) > 0 {
		return "Worker stop/evidence failures; terminals retained: " + strings.Join(failures, "; ")
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
		mWin := w.marshalTarget()
		mPane := w.tmuxMarshalPaneID
		w.tmuxMu.Unlock()

		_ = tmux.SelectWindow(ctx, mWin)
		if mPane != "" {
			_ = tmux.SelectPane(ctx, mPane)
		}
		return "View: showing only MARSHAL (focus).", nil

	case "side-by-side", "split":
		w.tmuxMu.Lock()
		active := copyAgentLocked(w.activeWorkerAgentLocked())
		w.tmuxMu.Unlock()
		if active == nil {
			return "No active worker session to show side-by-side.", nil
		}
		target := w.marshalTarget()
		if err := tmux.JoinPane(ctx, active.paneID, target, true); err != nil {
			return "", fmt.Errorf("show side-by-side: %w", err)
		}
		w.tmuxMu.Lock()
		if current := w.tmuxActiveWins[active.id]; current != nil {
			current.isJoined = true
		}
		w.tmuxMu.Unlock()
		return fmt.Sprintf("View: showing MARSHAL and %s side-by-side.", active.label), nil

	case "worker", "active":
		w.tmuxMu.Lock()
		active := copyAgentLocked(w.activeWorkerAgentLocked())
		w.tmuxMu.Unlock()
		if active == nil {
			return "No active worker session.", nil
		}
		if active.isJoined {
			if err := tmux.SelectPane(ctx, active.paneID); err != nil {
				return "", err
			}
		} else {
			if err := tmux.SelectWindow(ctx, active.paneID); err != nil {
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
			_ = tmux.SelectWindow(ctx, w.marshalTarget())
			return "View: showing MARSHAL.", nil
		case "chat":
			w.tmuxMu.Lock()
			chatAgent, exists := w.tmuxActiveWins["marshal-chat"]
			chatAgent = copyAgentLocked(chatAgent)
			w.tmuxMu.Unlock()
			if exists {
				_ = tmux.SelectWindow(ctx, chatAgent.paneID)
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
			agent = copyAgentLocked(agent)
			w.tmuxMu.Unlock()
			if exists {
				if agent.isJoined {
					_ = tmux.SelectPane(ctx, agent.paneID)
				} else {
					_ = tmux.SelectWindow(ctx, agent.paneID)
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
		mWin := w.marshalTarget()
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
		active := copyAgentLocked(w.activeWorkerAgentLocked())
		w.tmuxMu.Unlock()
		if active == nil {
			return "No active worker session.", nil
		}
		if err := tmux.SetPaneReadOnly(ctx, active.paneID, true); err != nil {
			return "", err
		}
		w.tmuxMu.Lock()
		if current := w.tmuxActiveWins[active.id]; current != nil {
			current.readOnly = true
		}
		w.tmuxMu.Unlock()
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
	active := copyAgentLocked(w.activeWorkerAgentLocked())
	w.tmuxMu.Unlock()
	if active == nil {
		return "No active worker session to take over.", nil
	}
	// Copy mode consumes input even when pane_input_off is clear.
	if _, err := tmux.RunCommand(ctx, "if-shell", "-F", "-t", active.paneID, "#{pane_in_mode}", "send-keys -X -t "+active.paneID+" cancel"); err != nil {
		return "", err
	}
	if err := tmux.SetPaneReadOnly(ctx, active.paneID, false); err != nil {
		return "", err
	}
	var err error
	if active.isJoined {
		err = tmux.SelectPane(ctx, active.paneID)
	} else {
		err = tmux.SelectWindow(ctx, active.paneID)
	}
	if err != nil {
		return "", err
	}
	// Selection hooks are not an input-readiness barrier: select-pane skips
	// its hook when this pane is already active. Restore each attached
	// client's table explicitly, and wait for those commands to complete.
	clients, err := tmux.RunCommand(ctx, "list-clients", "-t", w.tmuxSession, "-F", "#{client_name}")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(clients)) != "" {
		table, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", active.paneID, "#{@marshal_key_table}")
		if err != nil {
			return "", err
		}
		for _, client := range strings.Split(strings.TrimSpace(string(clients)), "\n") {
			if _, err := tmux.RunCommand(ctx, "switch-client", "-c", client, "-T", strings.TrimSpace(string(table))); err != nil {
				return "", err
			}
		}
	}
	w.tmuxMu.Lock()
	if current := w.tmuxActiveWins[active.id]; current != nil {
		current.readOnly = false
	}
	w.tmuxMu.Unlock()
	return fmt.Sprintf("Takeover: input enabled for %s. You can now type directly into the session. Press F11 to return to MARSHAL.", active.label), nil
}

func saveAgentEvidence(root, identifier, evidence string) error {
	dir := filepath.Join(root, ".marshal", "evidence")
	if strings.ContainsAny(identifier, "/\\") || identifier == "" {
		return fmt.Errorf("invalid evidence identity")
	}
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
		if _, ok := d.(*tmuxTaskDriver); !ok {
			service.Drivers[name] = &tmuxTaskDriver{inner: d, w: w, name: name}
		}
	}
	for name, d := range service.GovernedDrivers {
		if _, ok := d.(*tmuxTaskDriver); !ok {
			service.GovernedDrivers[name] = &tmuxTaskDriver{inner: d, w: w, name: name}
		}
	}
}

type tmuxTaskDriver struct {
	inner    driver.Driver
	w        *Workspace
	name     string
	monitors sync.Map // driver handles to notification completion channels
}

func (t *tmuxTaskDriver) Mode() marshal.WorkerMode {
	return t.inner.Mode()
}

func (t *tmuxTaskDriver) Launch(ctx context.Context, req driver.Request) (*driver.Handle, error) {
	root := t.w.workDir
	agentID := taskAgentID(req)
	if err := resetAgentOutcome(root, agentID); err != nil {
		return nil, err
	}
	agent := &activeTmuxAgent{id: agentID, role: "task", taskID: req.Task.PlanTaskID, provider: req.Task.Worker, runID: req.RunID, label: "Task " + req.Task.PlanTaskID + " (" + req.Task.Worker + ")", state: "working", readOnly: true, driver: t.inner, doneChan: make(chan struct{})}
	host := func(ctx context.Context, socket string) error {
		relay, err := exec.LookPath("socat")
		if err != nil {
			return err
		}
		name := tmux.TaskWindowName(strings.TrimPrefix(agentID, "task-"), root)
		if err := tmux.NewWindow(ctx, t.w.tmuxSession, name, req.Worktree, nil, []string{relay, "STDIO,raw,echo=0", "UNIX-CONNECT:" + socket}); err != nil {
			return err
		}
		t.w.tmuxMu.Lock()
		defer t.w.tmuxMu.Unlock()
		agent.window = name
		agent.paneID = t.w.tmuxSession + ":" + name
		panes, err := tmux.ListPanes(ctx, t.w.tmuxSession)
		if err != nil {
			return err
		}
		for _, p := range panes {
			if p.WindowName == name {
				agent.paneID = p.PaneID
				agent.windowID = p.WindowID
				break
			}
		}
		if err := tmux.SetWindowOption(ctx, agent.paneID, "remain-on-exit", "on"); err != nil {
			return err
		}
		if err := tmux.SetPaneReadOnly(ctx, agent.paneID, true); err != nil {
			return err
		}
		return t.w.bindWorkspaceKeysLocked(ctx, agent.paneID, root)
	}
	observer := func(ref processgroup.Reference) error {
		t.w.tmuxMu.Lock()
		defer t.w.tmuxMu.Unlock()
		agent.supervisor = ref
		return saveAgentRecord(root, t.w.tmuxSession, agent)
	}
	launchCtx := workerterminal.WithLifecycle(workerterminal.WithHost(ctx, host), observer)
	launchCtx = workerterminal.WithCompletion(launchCtx, agentCompletion(root, agentID))
	launchCtx = workerterminal.WithIdentity(launchCtx, func(id workerterminal.Identity) error {
		t.w.tmuxMu.Lock()
		defer t.w.tmuxMu.Unlock()
		agent.executionRunID = id.ExecutionRunID
		agent.canonicalTaskID = id.CanonicalTaskID
		return saveAgentRecord(root, t.w.tmuxSession, agent)
	})
	t.w.tmuxMu.Lock()
	t.w.tmuxActiveWins[agentID] = agent
	t.w.tmuxMu.Unlock()
	h, err := t.inner.Launch(launchCtx, req)
	if err != nil {
		t.w.tmuxMu.Lock()
		delete(t.w.tmuxActiveWins, agentID)
		t.w.tmuxMu.Unlock()
		if agent.paneID != "" {
			_ = tmux.KillPane(context.Background(), agent.paneID)
		}
		return nil, err
	}
	t.w.tmuxMu.Lock()
	agent.handle = h
	t.w.tmuxActiveWins[agentID] = agent
	t.w.tmuxMu.Unlock()
	t.w.updateTmuxStatusLine(ctx)
	t.monitors.Store(h, agent.doneChan)
	t.w.tmuxMonitors.Add(1)
	go func() { defer t.w.tmuxMonitors.Done(); defer close(agent.doneChan); t.w.monitorTaskState(agent, h) }()
	return h, nil
}

func (t *tmuxTaskDriver) Wait(ctx context.Context, h *driver.Handle) (marshal.HandIn, error) {
	handin, err := t.inner.Wait(ctx, h)
	t.joinMonitor(h)

	taskID := h.Request().Task.PlanTaskID
	agentID := taskAgentID(h.Request())
	root := t.w.workDir
	if t.w.runtime != nil && t.w.runtime.ProjectRoot() != "" {
		root = t.w.runtime.ProjectRoot()
	}

	t.w.tmuxMu.Lock()
	agent, ok := t.w.tmuxActiveWins[agentID]
	t.w.tmuxMu.Unlock()

	if ok && agent != nil {
		if closeErr := t.w.retainAndCloseAgent(context.Background(), agent, root, "completed"); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		t.w.updateTmuxStatusLine(context.Background())
		state := "done"
		message := "Task " + taskID + " completed."
		if err != nil {
			state = "failed"
			message = "Task " + taskID + " failed: " + err.Error()
		} else {
			for _, record := range handin.RuntimeObserved {
				if record.ExitCode != 0 {
					state = "failed"
					message = "Task " + taskID + " failed."
					break
				}
			}
		}
		if alertErr := t.w.deliverEgressAlert(app.EgressAlert{RunID: h.Request().RunID, ParentRunID: h.Request().RunID, TaskID: taskID, Worker: h.Request().Task.Worker, Kind: "task " + state, State: state, Message: message}); alertErr != nil {
			err = errors.Join(err, alertErr)
		}
	}

	return handin, err
}

func (t *tmuxTaskDriver) Cancel(h *driver.Handle) error {
	err := t.inner.Cancel(h)
	t.joinMonitor(h)
	return err
}

func (t *tmuxTaskDriver) joinMonitor(h *driver.Handle) {
	if done, ok := t.monitors.Load(h); ok {
		<-done.(chan struct{})
		t.monitors.Delete(h)
	}
}

// chatBinding pins restart to the conversation observed for this project.
type chatBinding struct {
	HistoryBaseline []string `json:"history_baseline"`
	Provider        string   `json:"provider"`
	Binary          string   `json:"binary"`
	Args            []string `json:"args"`
	Env             []string `json:"env"`
	SessionID       string   `json:"session_id"`
	RunID           string   `json:"run_id"`
}

func loadChatBinding(root string) chatBinding {
	var binding chatBinding
	data, err := os.ReadFile(filepath.Join(root, ".marshal", "tmux-chat.json"))
	if err == nil {
		_ = json.Unmarshal(data, &binding)
	}
	return binding
}
func (w *Workspace) saveChatBindingLocked(root string, a *activeTmuxAgent) error {
	if err := saveChatBinding(root, chatBinding{Provider: a.provider, Binary: a.binary, Args: a.args, Env: a.env, SessionID: a.sessionID, RunID: a.runID, HistoryBaseline: a.historyBaseline}); err != nil {
		return err
	}
	return saveAgentRecord(root, w.tmuxSession, a)
}
func saveChatBinding(root string, binding chatBinding) error {
	data, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, ".marshal")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".tmux-chat-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, "tmux-chat.json")); err != nil {
		return err
	}
	return nil
}
func (w *Workspace) captureChatConversation(root, id string) error {
	if id == "" {
		return nil
	}
	w.tmuxMu.Lock()
	a := w.tmuxActiveWins["marshal-chat"]
	if a == nil {
		w.tmuxMu.Unlock()
		return nil
	}
	// A restart must not adopt another native session in this project.
	if a.sessionID != "" && a.sessionID != id {
		w.tmuxMu.Unlock()
		return nil
	}
	a.sessionID = id
	err := w.saveChatBindingLocked(root, a)
	w.tmuxMu.Unlock()
	m := w.marshalSession()
	m.mu.Lock()
	m.conversationID = id
	m.mu.Unlock()
	return err
}

// Establish the existing history before launch so another conversation in the
// same directory cannot become this chat's durable resume identity.
func (w *Workspace) prepareChatHistoryWatch(root string, watch *nativeHistoryWatch, savedID string, recovered ...[]string) ([]string, error) {
	baseline := make(map[string]bool)
	if len(recovered) > 0 {
		for _, id := range recovered[0] {
			baseline[id] = true
		}
	}
	previous := watch.consume
	if previous == nil {
		previous = func(importer.SessionTranscript) error { return nil }
	}
	watch.observeSession = func(tr importer.SessionTranscript) error { baseline[tr.SessionID] = true; return nil }
	watch.consume = func(tr importer.SessionTranscript) error {
		baseline[tr.SessionID] = true
		return previous(tr)
	}
	// Legacy unbound records have no provenance: baseline all existing history.
	if len(recovered) == 0 || recovered[0] == nil {
		if err := watch.sync(); err != nil {
			return nil, err
		}
	}
	observe := func(tr importer.SessionTranscript) error {
		if savedID != "" && tr.SessionID != savedID || savedID == "" && baseline[tr.SessionID] {
			return nil
		}
		return w.captureChatConversation(root, tr.SessionID)
	}
	watch.observeSession = observe
	watch.consume = func(tr importer.SessionTranscript) error {
		if err := observe(tr); err != nil {
			return err
		}
		return previous(tr)
	}
	ids := make([]string, 0, len(baseline))
	for id := range baseline {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (w *Workspace) chatHistoryWatch(root, provider, binary string) *nativeHistoryWatch {
	var watch *nativeHistoryWatch
	switch provider {
	case "opencode":
		watch = newOpenCodeHistoryWatch(binary, root)
	case "antigravity", "agy":
		watch, _ = newAntigravityHistoryWatch(root)
	default:
		env, dir, history := "CODEX_HOME", ".codex", "sessions"
		if provider == "claude" {
			env, dir, history = "CLAUDE_CONFIG_DIR", ".claude", "projects"
		}
		home := os.Getenv(env)
		if home == "" {
			home, _ = os.UserHomeDir()
			home = filepath.Join(home, dir)
		}
		watch = newNativeHistoryWatch(filepath.Join(home, history), root)
		watch.claude = provider == "claude"
	}
	if watch == nil {
		watch = newNativeHistoryWatch("", root)
	}
	return watch
}

func taskAgentID(req driver.Request) string {
	if req.RunID != "" {
		return "task-" + req.RunID + "-" + req.Task.PlanTaskID
	}
	return "task-" + req.Task.PlanTaskID
}
