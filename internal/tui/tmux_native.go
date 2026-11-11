package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/nativehistory"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/workerterminal"
)

// inferStateFromTitle inspects the pane title for state hints when OSC 7501 is absent.
func inferStateFromTitle(title string) string {
	low := strings.ToLower(title)
	switch {
	case strings.Contains(low, "blocked") || strings.Contains(low, "waiting") || strings.Contains(low, "ask"):
		return "blocked"
	case strings.Contains(low, "done") || strings.Contains(low, "completed") || strings.Contains(low, "success"):
		return "done"
	case strings.Contains(low, "error") || strings.Contains(low, "failed"):
		return "error"
	case strings.Contains(low, "idle"):
		return "idle"
	default:
		return "working"
	}
}

// Launch origin is supplied by the command or dispatch boundary, never inferred
// from the provider or whether a launch changes focus.
type nativeLaunchOrigin string

const (
	nativeLaunchOperator  nativeLaunchOrigin = "operator"
	nativeLaunchAutomated nativeLaunchOrigin = "automated"
)

func (origin nativeLaunchOrigin) readOnly(isChat bool) bool {
	return !isChat && origin != nativeLaunchOperator
}

type activeTmuxAgent struct {
	launchOrigin    nativeLaunchOrigin
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
	mcpLaunchID     string
	historyPath     string
	runID           string
	driver          driver.Driver
	handle          *driver.Handle
	supervisor      processgroup.Reference
	cleanupMu       sync.Mutex
	cleaned         bool
	cleaning        bool
}

// Automatic setup uses the same launch pipeline as operator commands, but must
// never select an agent window. Explicit commands use an unmarked context.
type tmuxBackgroundLaunchKey struct{}

func tmuxLaunchInBackground(ctx context.Context) bool {
	background, _ := ctx.Value(tmuxBackgroundLaunchKey{}).(bool)
	return background
}

// Notes InitTmux leaves when tmux integration cannot start.
const (
	tmuxForeignSessionNote = "Marshal is running inside another tmux session, so the chat and worker windows can't open here. Quit (Ctrl+C twice) and run marshal from a terminal that isn't inside tmux."
	tmuxStateRefusedNote   = "Marshal can't use this folder's state directory. Run marshal doctor in a terminal to see why."
)

// InitTmux initializes tmux session and window awareness for the workspace.
func (w *Workspace) InitTmux(root ...string) {
	projectRoot := w.providerRoot()
	if len(root) > 0 && root[0] != "" {
		projectRoot = root[0]
	}
	bin, err := tmux.FindBinary()
	if err != nil {
		return
	}
	if err := statepath.Validate(projectRoot); err != nil {
		w.RecordActivity(tmuxStateRefusedNote)
		return
	}
	session, win, winID, pane := tmux.SessionName(projectRoot), statepath.Name, "", ""
	if tmux.IsInsideTmux() {
		if sess, name, id, err := tmux.CurrentSessionAndWindow(context.Background()); err == nil && sess != "" {
			if !tmux.SessionBelongs(sess, projectRoot) {
				w.RecordActivity(tmuxForeignSessionNote)
				return
			}
			session = sess
			if name != "" {
				win = name
			}
			winID = id
		}
		pane, _ = tmux.CurrentPaneID(context.Background())
	}
	w.tmuxMu.Lock()
	w.tmuxPath, w.tmuxSession, w.tmuxMarshalWin, w.tmuxMarshalWinID, w.tmuxMarshalPaneID = bin, session, win, winID, pane
	if w.tmuxActiveWins == nil {
		w.tmuxActiveWins = make(map[string]*activeTmuxAgent)
	}
	target := w.marshalTarget()
	w.tmuxMu.Unlock()
	if tmux.IsInsideTmux() {
		_ = w.bindWorkspaceKeys(context.Background(), pane, projectRoot)
	}
	w.recoverSurvivingWorkers(projectRoot)
	if tmux.IsInsideTmux() {
		_ = tmux.SelectWindow(context.Background(), target)
	}
	w.startPermissionQueue()
	// The Marshal chat starts only after the launch check finished ready (or
	// ready with warnings): the protocol says the Marshal is started only once
	// the doctor passed. afterLaunchCheck runs this once then, immediately if
	// the check is already ready, and never while the project is blocked.
	w.afterLaunchCheck(func() {
		if w.runtime == nil {
			w.ensureMarshalChatAuto(projectRoot)
		}
		if w.runtime != nil && tmux.IsInsideTmux() {
			if _, err := w.marshalChat(context.WithValue(context.Background(), tmuxBackgroundLaunchKey{}, true)); err != nil && !isProviderAuthError(err) {
				w.RecordActivity("Marshal chat: " + err.Error())
			}
		}
	})
	if err := w.reconcileStartupDock(context.Background()); err != nil {
		w.RecordActivity("Marshal dock: " + err.Error())
	}
}

// adoptSurvivingWorkers retains the caller-held lock contract used by recovery.
// Release it for IO and reacquire it before returning to the caller.
func (w *Workspace) adoptSurvivingWorkers(projectRoot string) {
	w.tmuxMu.Unlock()
	defer w.tmuxMu.Lock()
	w.recoverSurvivingWorkers(projectRoot)
}

func (w *Workspace) recoverSurvivingWorkers(projectRoot string) {
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
		w.tmuxMu.Lock()
		hosted := w.agentRecordHostedBy(record, p)
		w.tmuxMu.Unlock()
		if !hosted {
			// The recorded pane is gone; its ID now names an unrelated pane.
			_ = os.Remove(agentRecordPath(projectRoot, p.PaneID))
			continue
		}
		w.tmuxMu.Lock()
		_, exists := w.tmuxActiveWins[record.ID]
		w.tmuxMu.Unlock()
		if exists {
			continue
		}
		agent := &activeTmuxAgent{id: record.ID, role: record.Role, taskID: record.TaskID, runID: record.RunID, executionRunID: record.ExecutionRunID, canonicalTaskID: record.CanonicalTaskID, provider: record.Provider, label: record.Label, window: record.Window, windowID: p.WindowID, paneID: p.PaneID, pid: p.PID, state: "working", launchOrigin: record.LaunchOrigin, readOnly: record.LaunchOrigin.readOnly(record.Role == "marshal-chat"), supervisor: record.Supervisor, isJoined: p.WindowID == w.tmuxMarshalWinID}
		agentCtx, cancel := context.WithCancel(context.Background())
		agent.cancel = cancel
		agent.doneChan = make(chan struct{})
		var watch *nativeHistoryWatch
		var saved chatBinding
		var chatRecoveryErr error
		if record.Role == "marshal-chat" {
			saved = loadChatBinding(projectRoot)
			agent.mcpLaunchID = saved.MCPLaunchID
			agent.binary = saved.Binary
			agent.args = saved.Args
			agent.env = saved.Env
			agent.sessionID = saved.SessionID
			agent.historyPath = saved.HistoryPath
			agent.historyBaseline = saved.HistoryBaseline
			watch = w.chatHistoryWatch(projectRoot, agent.provider, agent.binary)
			baseline, err := w.prepareChatHistoryWatch(projectRoot, watch, saved.SessionID, saved.HistoryBaseline)
			if err == nil {
				agent.historyBaseline = baseline
			}
			if err != nil {
				chatRecoveryErr = err
				watch = nil
			}
		}
		w.tmuxMu.Lock()
		w.tmuxActiveWins[record.ID] = agent
		w.tmuxMu.Unlock()
		if record.Role == "marshal-chat" {
			if chatRecoveryErr == nil {
				chatRecoveryErr = w.restoreRecoveredChatMCP(agentCtx, projectRoot, agent, saved)
			}
			if chatRecoveryErr == nil {
				chatRecoveryErr = w.saveChatBindingForAgent(projectRoot, agent)
			}
			if chatRecoveryErr != nil {
				// Keep monitoring /exit and capturing a not-yet-known own
				// conversation, but never observe a foreign binding.
				if errors.Is(chatRecoveryErr, errChatMCPBindingMismatch) {
					watch = nil
				}
				w.tmuxMu.Lock()
				agent.state = "mcp-unavailable"
				w.tmuxMu.Unlock()
				w.chatMCPRecoveryFailed(projectRoot, chatRecoveryErr)
			}
		}
		_ = w.bindWorkspaceKeys(context.Background(), agent.paneID, projectRoot)
		w.monitorAgent(agentCtx, agent, projectRoot, nil, watch, nil, nil, nil)
	}
}

// ensureMarshalChatAuto opens the Marshal chat pane automatically if not already active (F3).
func (w *Workspace) ensureMarshalChatAuto(projectRoot string) {
	if w.tmuxSession == "" {
		return
	}
	w.tmuxMu.Lock()
	_, ok := w.tmuxActiveWins["marshal-chat"]
	w.tmuxMu.Unlock()
	if ok {
		return
	}
	chatWin := tmux.ChatWindowName(projectRoot)
	ctx := context.Background()
	exists, _ := tmux.WindowExists(ctx, w.tmuxSession, chatWin)
	if exists {
		w.recoverSurvivingWorkers(projectRoot)
		w.tmuxMu.Lock()
		_, ok := w.tmuxActiveWins["marshal-chat"]
		w.tmuxMu.Unlock()
		if ok {
			return
		}
	}
	if err := w.startMarshalChat(ctx, projectRoot); err != nil && !isProviderAuthError(err) {
		var start *marshalStartError
		if errors.As(err, &start) {
			w.logTUIError("marshal start", errors.Unwrap(err))
		}
		w.RecordActivity(err.Error())
	}
}

func (w *Workspace) startMarshalChat(ctx context.Context, projectRoot string) error {
	provider, _ := w.chooseMarshalProvider(ctx)
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
		return marshalNotStarted(provider, errors.New("protocol unavailable"))
	}
	args, env, dir, err := prepareMarshalLaunch(provider, projectRoot, nil, w.marshalContinuityBriefing(projectRoot, brief), false)
	if err == nil {
		args, env, dir, err = w.prepareProposalMCP(provider, projectRoot, args, env, dir)
	}
	if err != nil {
		return err
	}
	launched := false
	defer func() {
		if !launched {
			dir.remove()
		}
	}()

	saved := loadChatBinding(projectRoot)
	if canonicalNeutralProvider(saved.Provider) != canonicalNeutralProvider(provider) {
		saved.SessionID = ""
		saved.HistoryPath = ""
		saved.HistoryBaseline = nil
	}
	runID := saved.RunID
	m := w.marshalSession()
	m.mu.Lock()
	if m.runID != "" {
		runID = m.runID
	}
	m.mu.Unlock()
	if canonicalNeutralProvider(saved.Provider) == canonicalNeutralProvider(provider) && saved.SessionID != "" {
		args = append(marshalResumeArgs(provider, saved.SessionID), args...)
	}
	for _, key := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "HOME"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	watch := w.chatHistoryWatch(projectRoot, provider, binary)
	baseline, err := w.prepareChatHistoryWatch(projectRoot, watch, saved.SessionID)
	if err != nil {
		return err
	}
	if err := w.preflightProviderAuth(ctx, provider, binary, env); err != nil {
		return err
	}
	// Persist the prelaunch history even if the process crashes before binding.
	if err := saveChatBinding(projectRoot, chatBinding{Provider: provider, Binary: binary, Args: args, Env: env, SessionID: saved.SessionID, HistoryPath: saved.HistoryPath, RunID: runID, HistoryBaseline: baseline}); err != nil {
		return err
	}
	cmd := marshalChatCommand(binary, args)

	if err := tmux.NewWindow(ctx, w.tmuxSession, winName, projectRoot, env, cmd); err != nil {
		return marshalNotStarted(provider, fmt.Errorf("%s", RedactContent(err.Error(), nil)))
	}
	launched = true
	_ = tmux.SetWindowOption(ctx, w.tmuxSession+":"+winName, "remain-on-exit", "on")
	_ = tmux.SetPaneReadOnly(ctx, w.tmuxSession+":"+winName, false) // F3: normal operator input

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
		historyPath:     saved.HistoryPath,
		historyBaseline: baseline,
		runID:           runID,
	}

	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = agent
	w.tmuxMu.Unlock()
	w.activateProposalMCP(agent)
	_ = w.bindWorkspaceKeys(ctx, agent.paneID, projectRoot)
	_ = w.saveChatBindingForAgent(projectRoot, agent)
	w.monitorAgent(agentCtx, agent, projectRoot, dir, watch, nil, nil, nil)
	w.ensureMarshalDraftWatch(projectRoot, provider, runID)
	// A new chat window joins the Board's dock.
	w.kickDock()
	return nil
}

// marshalChatHiddenEnv removes the tmux client variables from the Marshal's
// process. With them, any shell command the Marshal runs reaches MARSHAL's own
// tmux server and could type into the owner's pane (tmux send-keys). The
// Marshal acts only through its MCP tools and proposals.
var marshalChatHiddenEnv = []string{"-u", "TMUX", "-u", "TMUX_PANE"}

// marshalChatCommand is the argv that starts the Marshal chat CLI without
// the tmux client variables.
func marshalChatCommand(binary string, args []string) []string {
	cmd := append([]string{"env"}, marshalChatHiddenEnv...)
	cmd = append(cmd, binary)
	return append(cmd, args...)
}

func (w *Workspace) restartMarshalChat(ctx context.Context, agent *activeTmuxAgent, root string) {
	if err := w.respawnMarshalChat(ctx, agent, root, true); err != nil {
		var start *marshalStartError
		if errors.As(err, &start) {
			w.logTUIError("marshal start", errors.Unwrap(err))
		}
		w.RecordActivity(err.Error())
	}
}

// Recovery uses an in-place respawn: falling back to another window could
// obscure a failed restore or resume the conversation in two provider panes.
func (w *Workspace) respawnMarshalChat(ctx context.Context, agent *activeTmuxAgent, root string, allowNewWindow bool) error {
	w.tmuxMu.Lock()
	original := agent
	agent = copyAgentLocked(agent)
	w.tmuxMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}

	provider := agent.provider
	if provider == "" {
		provider, _ = w.chooseMarshalProvider(ctx)
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

	brief, err := marshalRoleBriefing(app.MarshalWorkers(provider), marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		return marshalNotStarted(provider, errors.New("protocol unavailable"))
	}
	var base []string
	if sessionID != "" {
		base = marshalResumeArgs(provider, sessionID)
	}
	resumeArgs, env, dir, err := prepareMarshalLaunch(provider, root, base, w.marshalContinuityBriefing(root, brief), sessionID != "")
	if err == nil {
		resumeArgs, env, dir, err = w.prepareProposalMCP(provider, root, resumeArgs, env, dir)
	}
	if err != nil {
		dir.remove()
		return err
	}
	for _, prior := range agent.env {
		if strings.HasPrefix(prior, "CODEX_HOME=") || strings.HasPrefix(prior, "CLAUDE_CONFIG_DIR=") || strings.HasPrefix(prior, "HOME=") {
			env = append(env, prior)
		}
	}
	if err := w.preflightProviderAuth(ctx, provider, bin, env); err != nil {
		dir.remove()
		if !allowNewWindow {
			return err
		}
		_ = w.retainAndCloseAgent(ctx, original, root, "failed")
		if original.cancel != nil {
			original.cancel()
		}
		w.updateTmuxStatusLine(context.Background())
		return err
	}
	// Respawn inherits the old pane environment, so supply the rebuilt hidden
	// instruction configuration explicitly (especially OpenCode's file pointer).
	cmd := append([]string{"env"}, marshalChatHiddenEnv...)
	cmd = append(cmd, env...)
	cmd = append(cmd, bin)
	cmd = append(cmd, resumeArgs...)
	w.revokeProposalMCP(original)
	err = tmux.RespawnPaneIn(ctx, agent.paneID, root, cmd)
	if err != nil && allowNewWindow {
		err = tmux.NewWindow(ctx, w.tmuxSession, agent.window, root, env, marshalChatCommand(bin, resumeArgs))
	}
	if err != nil {
		dir.remove()
		return marshalNotStarted(provider, fmt.Errorf("%s", RedactContent(err.Error(), nil)))
	}
	agent.briefingDir.remove()
	agent.briefingDir = dir
	agent.args = resumeArgs
	agent.env = env

	if w.dockedChat() == agent.paneID {
		// Docked, the pane's window is the Board's: keep the option on the
		// chat pane alone, so the Board window keeps its own.
		_ = tmux.SetPaneOption(ctx, agent.paneID, "remain-on-exit", "on")
	} else {
		_ = tmux.SetWindowOption(ctx, agent.paneID, "remain-on-exit", "on")
	}
	_ = tmux.SetPaneReadOnly(ctx, agent.paneID, false) // F3: normal operator input

	panes, _ := tmux.ListPanes(ctx, w.tmuxSession)
	for _, p := range panes {
		if p.PaneID == agent.paneID || allowNewWindow && p.WindowName == agent.window {
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
	w.tmuxMu.Lock()
	original.binary, original.briefingDir, original.args, original.env = agent.binary, agent.briefingDir, agent.args, agent.env
	original.paneID, original.windowID, original.pid, original.pgid = agent.paneID, agent.windowID, agent.pid, agent.pgid
	original.state, original.readOnly = agent.state, agent.readOnly
	w.tmuxMu.Unlock()
	w.activateProposalMCP(original)
	_ = w.bindWorkspaceKeys(ctx, agent.paneID, root)
	w.tmuxMu.Lock()
	savedAgent := copyAgentLocked(original)
	w.tmuxMu.Unlock()
	if err := w.saveChatBindingForAgent(root, savedAgent); err != nil {
		w.revokeProposalMCP(original)
		return fmt.Errorf("Marshal chat binding not saved: %w", err)
	}
	w.ensureMarshalDraftWatch(root, provider, agent.runID)
	// A chat respawned in place stays docked; one recreated in a new window
	// is joined again, and the dock's keys are bound to it again (the
	// ordinary agent table was just bound over them).
	w.invalidateDockKeys(agent.paneID)
	w.kickDock()
	return nil
}

// ensureMarshalDraftWatch starts the draft watcher for a Marshal chat launch
// unless one is already running. Every launch path (automatic start, model
// switch, crash restart, /marshal chat) must import the Marshal's drafts; a
// chat whose drafts nobody imports stalls the whole flow. Without a runtime
// no draft can be imported, so nothing is started.
func (w *Workspace) ensureMarshalDraftWatch(root, provider, runID string) {
	if w.runtime == nil || !w.isTmuxActive() {
		return
	}
	m := w.marshalSession()
	m.mu.Lock()
	running := false
	if m.draftDone != nil {
		select {
		case <-m.draftDone:
		default:
			running = true
		}
	}
	if m.runID != "" {
		runID = m.runID
	}
	m.mu.Unlock()
	if running {
		return
	}
	if runID == "" {
		runID = loadChatBinding(root).RunID
	}
	if runID == "" {
		runID = fmt.Sprintf("RUN-%d", time.Now().UTC().UnixNano())
	}
	w.watchMarshalDraft(m, runID, root, provider, "")
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

// hostNativeTmuxWindow applies input policy before publishing the terminal.
func (w *Workspace) hostNativeTmuxWindow(ctx context.Context, name, root, socket string, origin nativeLaunchOrigin, paneID ...*string) error {
	relay, err := exec.LookPath("socat")
	if err != nil {
		return err
	}
	var target string
	argv := []string{relay, "STDIO,raw,echo=0", "UNIX-CONNECT:" + socket}
	if view, ok := ctx.Value(taskRelayViewKey{}).(taskRelayView); ok {
		// A governed task can host more than one process; retain all relay output.
		path := filepath.Join(view.root, statepath.DirName, "evidence", view.id+"-relay-latest.txt")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := saveAgentEvidence(view.root, view.id+"-relay", ""); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		argv = taskRelayCommand(relay, socket, view.root, view.id)
	}
	err = tmux.NewWindow(ctx, w.tmuxSession, name, root, nil, argv, &target)
	if err != nil {
		return err
	}
	if err := tmux.SetWindowOption(ctx, target, "remain-on-exit", "on"); err != nil {
		_ = tmux.KillPane(context.Background(), target)
		return err
	}
	if err := tmux.SetPaneReadOnly(ctx, target, origin.readOnly(false)); err != nil {
		_ = tmux.KillPane(context.Background(), target)
		return err
	}
	if len(paneID) > 0 {
		*paneID[0] = target
	}
	return nil
}

// runNativeAgentInTmux launches or switches to a native agent in its dedicated tmux window.
func (w *Workspace) runNativeAgentInTmux(
	ctx context.Context,
	origin nativeLaunchOrigin,
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
	readOnly := origin.readOnly(isChat)

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
	var operatorArgs []string
	if original, ok := ctx.Value(nativeRequestedArgsKey{}).([]string); ok {
		operatorArgs = original
	}
	if exists && winExists {
		requestedArgs := args
		if original, ok := ctx.Value(nativeRequestedArgsKey{}).([]string); ok {
			requestedArgs = original
		}
		if !isChat && len(requestedArgs) > 0 {
			return "", fmt.Errorf("Nothing was run: %s already has an active native session; arguments %q cannot be applied to it. Exit that session or stop it with Ctrl+X, then retry the command.", agentLabel, requestedArgs)
		}
		if isChat && canonicalNeutralProvider(existingAgent.provider) != canonicalNeutralProvider(provider) {
			runningProvider := existingAgent.provider
			if runningProvider == "" {
				runningProvider = "an unknown provider"
			}
			return "", fmt.Errorf("Marshal chat is still running %s. To switch to %s, run /marshal model %s. The existing chat has been left running.", runningProvider, provider, canonicalNeutralProvider(provider))
		}
		if isChat && existingAgent.state == "mcp-unavailable" {
			return "", errors.New("Marshal chat MCP is unavailable. " + chatMCPRepairInstructions)
		}
		if existingAgent != nil && existingAgent.state == "unavailable" && !shouldSkipAuthPreflight(operatorArgs, args) {
			if err := w.preflightProviderAuth(ctx, provider, binary, briefingEnv); err != nil {
				return "", err
			}
			w.tmuxMu.Lock()
			if current := w.tmuxActiveWins[agentID]; current != nil && current.state == "unavailable" {
				current.state = "working"
			}
			w.tmuxMu.Unlock()
		}
		// Only an operator command switches to a surviving window.
		if !tmuxLaunchInBackground(ctx) {
			if existingAgent != nil && existingAgent.isJoined {
				_ = tmux.SelectPane(ctx, existingAgent.paneID)
			} else {
				_ = tmux.SelectWindow(ctx, existingAgent.paneID)
			}
		}
		if !isChat {
			w.tmuxMu.Lock()
			w.nativeProvider = provider
			w.tmuxMu.Unlock()
		}
		w.updateTmuxStatusLine(ctx)
		return fmt.Sprintf("Switched to active %s session (tmux window %s). The session continues running. Press F11 to return to MARSHAL.", agentLabel, winName), nil
	}
	if !shouldSkipAuthPreflight(operatorArgs, args) {
		if err := w.preflightProviderAuth(ctx, provider, binary, briefingEnv); err != nil {
			dir.remove()
			return "", err
		}
	}

	// Resume only the conversation bound to this project.
	if isChat {
		saved := loadChatBinding(root)
		if canonicalNeutralProvider(saved.Provider) == canonicalNeutralProvider(provider) && saved.SessionID != "" {
			args = append(marshalResumeArgs(provider, saved.SessionID), args...)
		}
	}
	// The driver owns native worker execution; tmux hosts its terminal relay.
	var workerHandle *driver.Handle
	var sessionReference processgroup.Reference
	var hostedPane string
	if isChat {
		if err := tmux.NewWindow(ctx, w.tmuxSession, winName, root, briefingEnv, marshalChatCommand(binary, args), &hostedPane); err != nil {
			return "", err
		}
	} else {
		host := func(ctx context.Context, socket string) error {
			return w.hostNativeTmuxWindow(ctx, winName, root, socket, origin, &hostedPane)
		}
		if err := resetAgentOutcome(root, agentID); err != nil {
			return "", err
		}
		var err error
		sessionCtx := workerterminal.WithLifecycle(workerterminal.WithCompletion(workerterminal.WithHost(context.Background(), host), agentCompletion(root, agentID)), func(ref processgroup.Reference) error { sessionReference = ref; return nil })
		workerHandle, err = driver.LaunchSession(workerterminal.WithSize(sessionCtx, tmuxPaneSize(&hostedPane)), provider, binary, root, args, briefingEnv)
		if err != nil {
			return "", fmt.Errorf("launch %s in tmux: %w", agentLabel, err)
		}
	}

	// Hosting guarantees must hold before publishing a managed terminal.
	failHost := func(err error) (string, error) {
		if workerHandle != nil {
			_ = (driver.Native{}).Cancel(workerHandle)
		}
		_ = tmux.KillPane(context.Background(), hostedPane)
		return "", err
	}
	if err := tmux.SetWindowOption(ctx, hostedPane, "remain-on-exit", "on"); err != nil {
		return failHost(err)
	}
	if err := tmux.SetPaneReadOnly(ctx, hostedPane, readOnly); err != nil {
		return failHost(err)
	}

	// Discover immutable pane ID and PID
	panes, _ := tmux.ListPanes(ctx, w.tmuxSession)
	paneID := hostedPane
	pid := 0
	pgid := 0
	windowID := ""
	for _, p := range panes {
		if p.PaneID == hostedPane {
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

	if err := w.bindWorkspaceKeys(ctx, paneID, root); err != nil {
		return failHost(err)
	}

	// Bind provider's F-key and F11 in tmux (for workers only, not chat)
	if !isChat {
		fkey := providerFKey(provider)
		if fkey != "" {
			_ = tmux.BindWindowKey(ctx, paneID, statepath.Name+"-keys-"+tmux.ProjectHash(root), fkey, "select-window", "-t", paneID)
		}
	}

	// Install the return key and its selection hooks before exposing the window.
	if !tmuxLaunchInBackground(ctx) {
		if err := tmux.SelectWindow(ctx, paneID); err != nil {
			return failHost(err)
		}
	}

	agentCtx, cancel := context.WithCancel(context.Background())
	agent := &activeTmuxAgent{
		id:           agentID,
		role:         role,
		provider:     provider,
		label:        agentLabel,
		window:       winName,
		windowID:     windowID,
		paneID:       paneID,
		pid:          pid,
		pgid:         pgid,
		state:        "working",
		readOnly:     readOnly,
		launchOrigin: origin,
		cancel:       cancel,
		briefingDir:  dir,
		doneChan:     make(chan struct{}),
		binary:       binary,
		args:         args,
		env:          briefingEnv,
		handle:       workerHandle,
		supervisor:   sessionReference,
		driver:       driver.Native{},
	}

	if err := saveAgentRecord(root, w.tmuxSession, agent); err != nil {
		if workerHandle != nil {
			_ = agent.driver.Cancel(workerHandle)
		}
		return "", err
	}
	if isChat {
		saved := loadChatBinding(root)
		agent.sessionID = saved.SessionID
		agent.historyPath = saved.HistoryPath
		agent.historyBaseline = saved.HistoryBaseline
		_ = w.saveChatBindingForAgent(root, agent)
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins[agentID] = agent
	w.tmuxMu.Unlock()
	if isChat {
		w.activateProposalMCP(agent)
	}
	w.tmuxMu.Lock()
	if !isChat {
		w.nativeProvider = provider
	}
	w.tmuxMu.Unlock()
	w.updateTmuxStatusLine(ctx)

	w.monitorAgent(agentCtx, agent, root, dir, watch, peers, chStream, view)

	msg := fmt.Sprintf("Opened native %s in tmux window %s (view-only mode). Press F11 to return to MARSHAL. Use /takeover to enable typing.", agentLabel, winName)
	if !readOnly {
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
	if !w.workers.start(agentCtx, &w.tmuxMonitors, func(agentCtx context.Context) {
		defer close(agent.doneChan)
		if agent.role == "marshal-chat" {
			// A chat that ended for good leaves the dock.
			defer w.kickDock()
		}
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		authReported := false
		// A worker's OSC 7501 reports reach MARSHAL only through the pane's
		// raw output (capture-pane drops them), so the pane is tapped for as
		// long as it is monitored. The Marshal chat reports to no lane.
		if agent.role != "marshal-chat" {
			w.tmuxMu.Lock()
			pane := agent.paneID
			w.tmuxMu.Unlock()
			if pane != "" {
				defer w.tapPane(agentCtx, pane)()
			}
		}
		// The title heuristic is read every few ticks: it runs a tmux
		// command, and a monitor runs per agent window.
		laneTicks := 0
		for {
			select {
			case <-agentCtx.Done():
				return
			case <-ticker.C:
				w.tmuxMu.Lock()
				snapshot := copyAgentLocked(agent)
				w.tmuxMu.Unlock()
				// Poll syncs
				if snapshot.role == "marshal-chat" {
					w.observeMarshalProposalFiles(root)
					// A published draft must never wait for a relaunch: if no
					// watcher is importing it, start one (a no-op while running).
					if marshalDraftReady(root) {
						w.ensureMarshalDraftWatch(root, snapshot.provider, snapshot.runID)
					}
				}
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
					laneTicks++
					if st, ok := w.paneStatusOf(snapshot.paneID); ok && snapshot.paneID != "" {
						// The worker's own report wins over the title.
						status.State, status.LastAction = st.State, st.laneLast()
					} else if snapshot.paneID != "" && laneTicks%5 == 1 {
						if status.State == "" {
							if title, err := tmux.GetPaneTitle(agentCtx, snapshot.paneID); err == nil && title != "" {
								status.LastAction = title
								status.State = inferStateFromTitle(title)
							}
						}
					}
					_ = writeLiveStatus(root, snapshot.provider, status)
				}

				// Check whether pane is dead
				dead, exitCode, err := tmux.PaneDeadStatus(agentCtx, snapshot.paneID)
				// A clean exit of a pane that still exists is the operator
				// leaving the Marshal chat (e.g. /exit); a crash restarts it.
				closedByOperator := err == nil && dead && exitCode == 0
				if err != nil {
					// Resolve absence by immutable identity, including renamed windows.
					panes, listErr := tmux.ListPanes(agentCtx, w.tmuxSession)
					missing := listErr == nil
					for _, p := range panes {
						if p.PaneID == snapshot.paneID {
							missing = false
							break
						}
					}
					if missing && snapshot.role != "marshal-chat" {
						if err := w.retainAndCloseAgent(agentCtx, agent, root, "closed externally"); err != nil {
							w.RecordActivity(err.Error())
							continue
						}
						if dir != nil {
							dir.remove()
						}
						w.updateTmuxStatusLine(context.Background())
						return
					}
					if missing {
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
					if isAuthLoginSubcommand(snapshot.args) && exitCode == 0 {
						w.clearProviderAuthUnavailable(snapshot.provider)
					}

					authExit := authReported
					if !authExit && !closedByOperator && !shouldSkipAuthPreflight(nil, snapshot.args) {
						if snapshot.handle != nil {
							if record, outcomeErr, ok := snapshot.handle.Outcome(); ok {
								if detectProviderAuthFailure(snapshot.provider, record.Output) ||
									(outcomeErr != nil && detectProviderAuthFailure(snapshot.provider, outcomeErr.Error())) {
									authExit = true
								}
							}
						}
						if !authExit && snapshot.paneID != "" {
							if paneText, capErr := tmux.CapturePane(agentCtx, snapshot.paneID); capErr == nil && detectProviderAuthFailure(snapshot.provider, paneText) {
								authExit = true
							}
						}
					}

					if snapshot.role == "marshal-chat" {
						if agentCtx.Err() != nil {
							return
						}
						w.returnFromExitedAgent(agentCtx, snapshot)
						if closedByOperator {
							if err := w.retainAndCloseAgent(agentCtx, agent, root, "closed by operator"); err != nil {
								w.RecordActivity(err.Error())
								continue
							}
							w.RecordActivity(marshalChatClosedMessage(root))
							w.updateTmuxStatusLine(context.Background())
							return
						}
						if snapshot.state == "mcp-unavailable" {
							if err := w.retainAndCloseAgent(agentCtx, agent, root, "failed"); err != nil {
								w.RecordActivity(err.Error())
								continue
							}
							w.chatMCPRecoveryFailed(root, errors.New("the unrecovered chat exited"))
							return
						}
						if authExit {
							if err := w.retainAndCloseAgent(agentCtx, agent, root, "failed"); err != nil {
								w.RecordActivity(err.Error())
								continue
							}
							if dir != nil {
								dir.remove()
							}
							w.markProviderAuthUnavailable(snapshot.provider, providerNotSignedInMessage(snapshot.provider))
							w.updateTmuxStatusLine(context.Background())
							return
						}
						if err := w.preflightProviderAuth(agentCtx, snapshot.provider, snapshot.binary, snapshot.env); err != nil {
							if closeErr := w.retainAndCloseAgent(agentCtx, agent, root, "failed"); closeErr != nil {
								w.RecordActivity(closeErr.Error())
								continue
							}
							if dir != nil {
								dir.remove()
							}
							w.updateTmuxStatusLine(context.Background())
							return
						}
						w.RecordActivity("Marshal chat ended; restarting automatically and resuming the conversation. Use /marshal chat to reopen it.")
						w.restartMarshalChat(withAuthPreflightChecked(agentCtx, snapshot.provider), agent, root)
						w.updateTmuxStatusLine(context.Background())
						continue
					}

					w.returnFromExitedAgent(agentCtx, snapshot)

					state := "done"
					if exitCode != 0 {
						state = "failed"
					}
					if snapshot.runID != "" {
						alertMsg := snapshot.label + " worker " + state + "."
						if authExit {
							alertMsg = providerNotSignedInMessage(snapshot.provider)
						}
						if err := w.deliverEgressAlert(app.EgressAlert{RunID: snapshot.runID, ParentRunID: snapshot.runID, TaskID: snapshot.taskID, Worker: snapshot.provider, Kind: "worker " + state, State: state, Message: alertMsg}); err != nil {
							w.RecordActivity(err.Error())
						}
					}
					if err := w.retainAndCloseAgent(context.Background(), agent, root, state); err != nil {
						w.RecordActivity(err.Error())
						continue
					}
					if dir != nil {
						dir.remove()
					}
					if authExit {
						w.markProviderAuthUnavailable(snapshot.provider, providerNotSignedInMessage(snapshot.provider))
					} else {
						w.RecordActivity(fmt.Sprintf("%s session ended (exit %d). Output saved to evidence.", snapshot.label, exitCode))
					}
					w.updateTmuxStatusLine(context.Background())
					return
				}

				if snapshot.paneID != "" && !shouldSkipAuthPreflight(nil, snapshot.args) {
					if visible, capErr := tmux.CaptureVisiblePane(agentCtx, snapshot.paneID); capErr == nil {
						if detectProviderAuthFailure(snapshot.provider, visible) {
							if !authReported {
								authReported = true
								w.tmuxMu.Lock()
								if agent.state != "unavailable" {
									agent.state = "unavailable"
								}
								w.tmuxMu.Unlock()
								w.markProviderAuthUnavailable(snapshot.provider, providerNotSignedInMessage(snapshot.provider))
							}
						} else if authReported {
							authReported = false
							w.tmuxMu.Lock()
							if agent.state == "unavailable" {
								agent.state = "working"
							}
							w.tmuxMu.Unlock()
							w.clearProviderAuthUnavailable(snapshot.provider)
						}
					}
				}

			}
		}
	}) {
		close(agent.doneChan)
	}
}

// Evaluate focus in tmux itself so navigation cannot race a query followed by
// an unconditional select. A failed/missing target never steals focus.
func (w *Workspace) returnFromExitedAgent(ctx context.Context, agent *activeTmuxAgent) {
	if agent.role != "marshal-chat" && agent.launchOrigin != nativeLaunchOperator {
		return
	}
	w.tmuxMu.Lock()
	session, control := w.tmuxSession, w.tmuxMarshalWinID
	if control == "" {
		control = w.tmuxMarshalWin
	}
	w.tmuxMu.Unlock()
	if session == "" || control == "" || agent.windowID == "" || agent.windowID == control {
		return
	}
	_, _ = tmux.RunCommand(ctx, "if-shell", "-t", session+":"+agent.windowID, "-F", "#{window_active}", fmt.Sprintf("select-window -t %q", session+":"+control))
}

// RecordActivity records activity into workspace state and triggers a redraw if interactive.
//
// It is the background channel: on the Board the message becomes one
// Activity line (the whole text a click away) and never opens the ANSWER
// overlay or replaces the footer, which answer only what the operator did.
func (w *Workspace) RecordActivity(msg string) {
	msg = hideMarshalProtocol(RedactContent(msg, nil))
	// Consecutive reroutes of the same provider pair within a minute collapse
	// into one line ("… I moved 2 tasks to …"); the newest entry is replaced
	// in place (board_quota.go).
	if collapsed, prev, ok := w.collapseReroute(msg); ok {
		w.mu.Lock()
		if w.state.LastOutput != "" && strings.HasSuffix(w.state.LastOutput, prev) {
			w.state.LastOutput = strings.TrimSuffix(w.state.LastOutput, prev) + collapsed
		}
		w.mu.Unlock()
		w.requestRepaint()
		return
	}
	w.addNote(msg, false)
	w.mu.Lock()
	if w.state.LastOutput != "" {
		w.state.LastOutput += "\n" + msg
	} else {
		w.state.LastOutput = msg
	}
	w.state.LastOutput = activityTail(w.state.LastOutput)
	w.mu.Unlock()
	w.requestRepaint()
}

// updateTmuxStatusLine updates the tmux status bar with the states of all active agents.
func (w *Workspace) updateTmuxStatusLine(ctx context.Context) {
	// Serialize before taking the snapshot: an older projection must not
	// finish after a newer one. Status reads never initialize another chat.
	w.tmuxStatusDirty.Store(true)
	if !w.tmuxStatusBusy.CompareAndSwap(false, true) {
		return
	}

	for {
		w.tmuxStatusDirty.Store(false)
		w.tmuxMu.Lock()
		if w.tmuxPath == "" || w.tmuxSession == "" {
			w.tmuxMu.Unlock()
			w.tmuxStatusBusy.Store(false)
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
		if buildMarker != "" {
			parts = append([]string{buildMarker}, parts...)
		}
		if len(parts) > 0 {
			statusText = fmt.Sprintf(" [%s] ", strings.Join(parts, " | "))
		}
		_ = tmux.SetWindowStatus(ctx, target, RedactContent(statusText, nil))
		if ctx.Err() != nil {
			w.tmuxStatusBusy.Store(false)
			return
		}
		if !w.tmuxStatusDirty.Load() {
			w.tmuxStatusBusy.Store(false)
			if !w.tmuxStatusDirty.Load() || !w.tmuxStatusBusy.CompareAndSwap(false, true) {
				return
			}
		}
	}
}

// reportActiveTmuxSessions prints still-running agent sessions when MARSHAL exits.
func (w *Workspace) reportActiveTmuxSessions() {
	if !w.isTmuxActive() {
		return
	}
	w.tmuxMu.Lock()
	var agents []*activeTmuxAgent
	for _, a := range w.tmuxActiveWins {
		agents = append(agents, copyAgentLocked(a))
	}
	session := w.tmuxSession
	w.tmuxMu.Unlock()
	if len(agents) == 0 {
		return
	}
	fmt.Fprintln(w.out, "\nActive native agent sessions still running in tmux:")
	for _, agent := range agents {
		fmt.Fprintf(w.out, "  • %s (window: %s)\n", agent.label, agent.window)
	}
	fmt.Fprintf(w.out, "Re-attach to this layout anytime with: tmux attach-session -t %s\n", session)
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
			select {
			case <-agent.doneChan:
			case <-ctx.Done():
				return "Worker stop cancelled while waiting for monitor: " + ctx.Err().Error()
			}
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
	// The Board and the classic screen are this terminal's own views and
	// need no tmux; nor does the native chat.
	if len(args) >= 1 && strings.EqualFold(args[0], "chat") {
		return w.handleViewChat(ctx, args[1:])
	}
	if len(args) == 1 {
		switch strings.ToLower(args[0]) {
		case "board":
			return w.setBoardView(false), nil
		case "classic":
			return w.setBoardView(true), nil
		}
	}
	if !w.isTmuxActive() {
		return "View commands require MARSHAL to run inside tmux.", nil
	}
	if len(args) == 0 {
		return "Usage: /view [board | classic | chat [native|tmux] | focus | side-by-side | worker | show <agent> | hide | follow | readonly | takeover]", nil
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "focus", "marshal":
		// Break any joined panes back out into their windows so only Marshal is visible
		joined := w.joinedWorkers()
		w.tmuxMu.Lock()
		mWin := w.marshalTarget()
		mPane := w.tmuxMarshalPaneID
		w.tmuxMu.Unlock()
		if err := w.breakJoinedPanes(ctx, joined); err != nil {
			return "", err
		}

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
			if exists && w.dockedChat() == chatAgent.paneID {
				_ = tmux.SelectPane(ctx, chatAgent.paneID)
				return "View: Marshal Chat focused beside the Board. Press F11 to return to the Board.", nil
			}
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
		joined := w.joinedWorkers()
		w.tmuxMu.Lock()
		mWin := w.marshalTarget()
		w.tmuxMu.Unlock()
		if err := w.breakJoinedPanes(ctx, joined); err != nil {
			return "", err
		}
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
		return "Usage: /view [board | classic | chat [native|tmux] | focus | side-by-side | worker | show <agent> | hide | follow | readonly | takeover]", nil
	}
}

// handleTakeoverCommand enables input in the active worker pane.
func (w *Workspace) handleTakeoverCommand(ctx context.Context, args ...string) (string, error) {
	if !w.isTmuxActive() {
		return "Takeover requires MARSHAL to run inside tmux.", nil
	}
	// Reject missing identities before issuing even a read-only tmux query.
	w.tmuxMu.Lock()
	hasTarget := false
	for _, a := range w.tmuxActiveWins {
		if a.role != "marshal-chat" && a.launchOrigin != nativeLaunchOperator && strings.TrimSpace(a.paneID) != "" && (len(args) == 0 || a.id == args[0]) {
			hasTarget = true
			break
		}
	}
	w.tmuxMu.Unlock()
	if !hasTarget {
		return "", errors.New("select a worker pane or use /takeover <agent>; worker must have a pane target")
	}
	selected, _ := tmux.RunCommand(ctx, "display-message", "-p", "-t", w.tmuxSession, "#{pane_id}")
	w.tmuxMu.Lock()
	var active *activeTmuxAgent
	var candidates []*activeTmuxAgent
	for _, a := range w.tmuxActiveWins {
		if a.role == "marshal-chat" || a.launchOrigin == nativeLaunchOperator {
			continue
		}
		if len(args) > 0 {
			if a.id == args[0] {
				active = copyAgentLocked(a)
			}
		} else if a.paneID == strings.TrimSpace(string(selected)) {
			active = copyAgentLocked(a)
		}
		if a.readOnly {
			candidates = append(candidates, a)
		}
	}
	if len(args) == 0 && active == nil && len(candidates) == 1 {
		active = copyAgentLocked(candidates[0])
	}
	w.tmuxMu.Unlock()
	if active == nil {
		return "", errors.New("select a worker pane or use /takeover <agent>")
	}
	if strings.TrimSpace(active.paneID) == "" {
		return "", errors.New("worker has no pane target")
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
	dir := filepath.Join(root, statepath.DirName, "evidence")
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

// watchMarshalDraft imports every draft the Marshal publishes while its chat
// is alive. It is started for each Marshal chat launch. A rejected draft is
// reported with its exact reason and the watcher keeps running, so the
// Marshal's corrected draft is imported without any owner step. It ends when
// the chat is gone, when it is replaced, or after a draft was imported (plan
// approval then owns the run).
func (w *Workspace) watchMarshalDraft(m *marshalSession, runID, root, provider, note string) {
	m.mu.Lock()
	previousCancel, previousDone := m.draftCancel, m.draftDone
	m.mu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	if previousDone != nil {
		<-previousDone
	}
	ctx, cancel := context.WithCancel(w.workers.context())
	done := make(chan struct{})
	m.mu.Lock()
	m.draftCancel, m.draftDone = cancel, done
	m.mu.Unlock()
	if !w.startBackground(ctx, func(ctx context.Context) {
		defer cancel()
		defer close(done)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
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
				if w.importWatchedMarshalDraft(ctx, m, runID, provider, data) {
					return
				}
			}
		}
	}) {
		cancel()
		close(done)
	}
}

// importWatchedMarshalDraft imports one consumed draft. It reports true when
// the draft became the canonical Process 04 plan. A rejection is published
// with its reason and leaves the watcher running for the next draft.
func (w *Workspace) importWatchedMarshalDraft(ctx context.Context, m *marshalSession, runID, provider string, data []byte) bool {
	m.mu.Lock()
	service := m.service
	m.mu.Unlock()
	if service == nil {
		var err error
		if service, _, _, err = w.marshalService(ctx, runID); err != nil {
			w.marshalDraftRejected(m, runID, provider, "draft validation failed", err)
			return false
		}
		m.mu.Lock()
		if m.service == nil {
			m.service = service
		}
		if m.runID == "" {
			m.runID = runID
		}
		m.mu.Unlock()
	}
	// Rejections are reported on the watched run: the panel and chat follow
	// that run, and a superseding run ID exists only once the draft is in.
	watchedRunID := runID
	_, snapErr := service.Snapshot(ctx, runID)
	superseding := draftSupersedesRun(snapErr)
	if superseding {
		runID = fmt.Sprintf("RUN-%d", time.Now().UTC().UnixNano())
	}
	packDir, packErr := service.TakePlanPack(runID)
	draft, err := service.DraftFromProposal(data, provider)
	if err != nil {
		w.marshalDraftRejected(m, watchedRunID, provider, "draft validation failed", err)
		return false
	}
	if packErr != nil {
		w.marshalDraftRejected(m, watchedRunID, provider, "plan pack transfer failed", packErr)
		return false
	}
	ids := make([]string, 0, len(draft.Tasks))
	for _, task := range draft.Tasks {
		ids = append(ids, task.PlanTaskID)
	}
	pack, err := app.ReadPlanPack(packDir, ids)
	if err != nil {
		w.marshalDraftRejected(m, watchedRunID, provider, "plan pack validation failed", err)
		return false
	}
	draft.Pack = &pack
	run, err := w.persistNativePlanningDraft(ctx, provider, service, runID, draft, marshal.Budget{})
	if err != nil {
		w.marshalDraftRejected(m, watchedRunID, provider, "canonical Process 04 evidence unavailable", err)
		return false
	}
	if superseding {
		w.cancelSupersededTaskLanes(ctx, run.PlanID)
	}
	m.mu.Lock()
	m.runID, m.service, m.provider, m.amended, m.pending = runID, service, provider, false, nil
	m.approvals = nil
	m.mu.Unlock()
	// The chat binding names the run a restarted workspace recovers. A
	// superseding run left it on the replaced run, which then took the panel
	// and the approval questions after every restart.
	w.tmuxMu.Lock()
	if a := w.tmuxActiveWins["marshal-chat"]; a != nil {
		a.runID = runID
		snapshot := copyAgentLocked(a)
		w.tmuxMu.Unlock()
		if err := w.saveChatBindingForAgent(service.Repository, snapshot); err != nil {
			w.RecordActivity("Marshal chat binding not updated: " + err.Error())
		}
	} else {
		w.tmuxMu.Unlock()
		if saved := loadChatBinding(service.Repository); saved.SessionID != "" {
			saved.RunID = runID
			if err := saveChatBinding(service.Repository, saved); err != nil {
				w.RecordActivity("Marshal chat binding not updated: " + err.Error())
			}
		}
	}
	w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, fmt.Sprintf("plan ready: %d tasks", len(run.Tasks))))
	w.RecordActivity(fmt.Sprintf("A plan with %d tasks is ready. Review it and approve it in the popup.", len(run.Tasks)))
	w.queueMarshalPlanApproval()
	return true
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
	if req.Task.ImportedResult == nil && canonicalAuthProvider(req.Task.Worker) != "" {
		var binary string
		if nativeDrv, ok := t.inner.(driver.Native); ok {
			binary = nativeDrv.Binary
		}
		if err := t.w.preflightProviderAuth(ctx, req.Task.Worker, binary, nil); err != nil {
			return nil, err
		}
	}
	root := t.w.workDir
	agentID := taskAgentID(req)
	if err := resetAgentOutcome(root, agentID); err != nil {
		return nil, err
	}
	agent := &activeTmuxAgent{id: agentID, role: "task", launchOrigin: nativeLaunchAutomated, taskID: req.Task.PlanTaskID, provider: req.Task.Worker, runID: req.RunID, label: "Task " + req.Task.PlanTaskID + " (" + req.Task.Worker + ")", state: "working", readOnly: true, driver: t.inner, doneChan: make(chan struct{})}
	host := func(ctx context.Context, socket string) error {
		name := tmux.TaskWindowName(strings.TrimPrefix(agentID, "task-"), root)
		if t.inner.Mode() == marshal.Governed {
			ctx = withTaskRelayView(ctx, root, agentID)
		}
		if err := t.w.hostNativeTmuxWindow(ctx, name, req.Worktree, socket, nativeLaunchAutomated, &agent.paneID); err != nil {
			return err
		}
		agent.window = name
		panes, err := tmux.ListPanes(ctx, t.w.tmuxSession)
		if err != nil {
			return err
		}
		for _, p := range panes {
			if p.PaneID == agent.paneID {
				agent.paneID = p.PaneID
				agent.windowID = p.WindowID
				agent.pid = p.PID
				break
			}
		}
		// Only the terminal host creates an active pane record. Imported
		// results run checks without ever invoking this host.
		t.w.tmuxMu.Lock()
		t.w.tmuxActiveWins[agentID] = agent
		t.w.tmuxMu.Unlock()
		if err := tmux.SetWindowOption(ctx, agent.paneID, "remain-on-exit", "on"); err != nil {
			return err
		}
		if err := tmux.SetPaneReadOnly(ctx, agent.paneID, true); err != nil {
			return err
		}
		return t.w.bindWorkspaceKeys(ctx, agent.paneID, root)
	}
	observer := func(ref processgroup.Reference) error {
		t.w.tmuxMu.Lock()
		agent.supervisor = ref
		snapshot, session := copyAgentLocked(agent), t.w.tmuxSession
		t.w.tmuxMu.Unlock()
		return saveAgentRecord(root, session, snapshot)
	}
	launchCtx := workerterminal.WithLifecycle(workerterminal.WithHost(ctx, host), observer)
	launchCtx = workerterminal.WithCompletion(launchCtx, agentCompletion(root, agentID))
	launchCtx = workerterminal.WithIdentity(launchCtx, func(id workerterminal.Identity) error {
		t.w.tmuxMu.Lock()
		agent.executionRunID = id.ExecutionRunID
		agent.canonicalTaskID = id.CanonicalTaskID
		snapshot, session := copyAgentLocked(agent), t.w.tmuxSession
		t.w.tmuxMu.Unlock()
		return saveAgentRecord(root, session, snapshot)
	})
	driverToLaunch := configureWorkerDriverExtreme(t.inner, root, agent)
	h, err := driverToLaunch.Launch(launchCtx, req)
	if err != nil {
		t.w.tmuxMu.Lock()
		delete(t.w.tmuxActiveWins, agentID)
		pane := agent.paneID
		t.w.tmuxMu.Unlock()
		if pane != "" {
			_ = tmux.KillPane(context.Background(), pane)
		}
		return nil, err
	}
	t.w.tmuxMu.Lock()
	agent.handle = h
	t.w.tmuxMu.Unlock()
	t.w.updateTmuxStatusLine(ctx)
	t.monitors.Store(h, agent.doneChan)
	if !t.w.workers.start(ctx, &t.w.tmuxMonitors, func(ctx context.Context) { defer close(agent.doneChan); t.w.monitorTaskAuthAndState(ctx, agent, h) }) {
		close(agent.doneChan)
	}
	return h, nil
}

func (t *tmuxTaskDriver) Wait(ctx context.Context, h *driver.Handle) (marshal.HandIn, error) {
	handin, err := t.inner.Wait(ctx, h)
	t.joinMonitor(h)

	taskID := h.Request().Task.PlanTaskID
	worker := h.Request().Task.Worker
	agentID := taskAgentID(h.Request())
	root := t.w.workDir
	if t.w.runtime != nil && t.w.runtime.ProjectRoot() != "" {
		root = t.w.runtime.ProjectRoot()
	}

	hasFailure := err != nil
	for _, record := range handin.RuntimeObserved {
		if record.ExitCode != 0 {
			hasFailure = true
			break
		}
	}
	authFailed := false
	if err != nil && detectProviderAuthFailure(worker, err.Error()) {
		authFailed = true
	}
	for _, record := range handin.RuntimeObserved {
		if (record.ExitCode != 0 || err != nil) && detectProviderAuthFailure(worker, record.Output) {
			authFailed = true
			break
		}
	}

	t.w.tmuxMu.Lock()
	agent, ok := t.w.tmuxActiveWins[agentID]
	approvalID, waiting := "", false
	if ok && agent != nil {
		approvalID, waiting = taskHandleApprovalWait(agent, h, err)
	}
	var agentPane string
	var agentState string
	if ok && agent != nil {
		agentPane = agent.paneID
		agentState = agent.state
	}
	t.w.tmuxMu.Unlock()

	if agentState == "unavailable" {
		authFailed = true
	}
	if !authFailed && hasFailure && strings.TrimSpace(agentPane) != "" {
		if paneText, capErr := tmux.CapturePane(context.Background(), agentPane); capErr == nil && detectProviderAuthFailure(worker, paneText) {
			authFailed = true
		}
	}
	if authFailed {
		t.w.markProviderAuthUnavailable(worker, providerNotSignedInMessage(worker))
	}

	if ok && agent != nil {
		// An orchestration handle may end while its canonical task waits for
		// approval. Its provider turn/pane is retained for that exact continuation.
		if !waiting {
			if closeErr := t.w.retainAndCloseAgent(context.Background(), agent, root, "completed"); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}
		t.w.updateTmuxStatusLine(context.Background())
		state := "done"
		message := "Task " + taskID + " completed."
		if waiting {
			state = "waiting"
			message = taskApprovalNotice(taskID, approvalID)
		} else if err != nil {
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
		if authFailed && state == "failed" {
			message = "Task " + taskID + " failed: " + providerNotSignedInMessage(worker)
		}
		if alertErr := t.w.deliverEgressAlert(app.EgressAlert{RunID: h.Request().RunID, ParentRunID: h.Request().RunID, TaskID: taskID, Worker: worker, Kind: "task " + state, State: state, Message: message}); alertErr != nil {
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
	// Launch identity detects a broker from a previous TUI; scope pins the
	// binding to the exact project, tmux session and provider pane.
	MCPLaunchID     string   `json:"mcp_launch_id,omitempty"`
	Project         string   `json:"project,omitempty"`
	TmuxSession     string   `json:"tmux_session,omitempty"`
	PaneID          string   `json:"pane_id,omitempty"`
	Namespace       string   `json:"state_namespace,omitempty"`
	HistoryBaseline []string `json:"history_baseline"`
	Provider        string   `json:"provider"`
	Binary          string   `json:"binary"`
	Args            []string `json:"args"`
	Env             []string `json:"env"`
	SessionID       string   `json:"session_id"`
	HistoryPath     string   `json:"history_path,omitempty"`
	RunID           string   `json:"run_id"`
}

func loadChatBinding(root string) chatBinding {
	var binding chatBinding
	if statepath.CheckPath(root, statepath.Path(root, "tmux-chat.json")) != nil {
		return binding
	}
	data, err := os.ReadFile(filepath.Join(root, statepath.DirName, "tmux-chat.json"))
	if err == nil {
		_ = json.Unmarshal(data, &binding)
		if binding.Namespace != statepath.DirName && (statepath.PublicEngine || binding.Namespace != "") {
			return chatBinding{}
		}
	}
	return binding
}
func (w *Workspace) saveChatBindingForAgent(root string, a *activeTmuxAgent) error {
	if err := saveChatBinding(root, chatBinding{MCPLaunchID: a.mcpLaunchID, Project: tmux.ProjectHash(root), TmuxSession: w.tmuxSession, PaneID: a.paneID, Provider: a.provider, Binary: a.binary, Args: a.args, Env: a.env, SessionID: a.sessionID, HistoryPath: a.historyPath, RunID: a.runID, HistoryBaseline: a.historyBaseline}); err != nil {
		return err
	}
	return saveAgentRecord(root, w.tmuxSession, a)
}
func saveChatBinding(root string, binding chatBinding) error {
	binding.Namespace = statepath.DirName
	if err := statepath.CheckPath(root, statepath.Path(root, "tmux-chat.json")); err != nil {
		return err
	}
	data, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	dir := statepath.Path(root)
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
	snapshot := copyAgentLocked(a)
	w.tmuxMu.Unlock()
	err := w.saveChatBindingForAgent(root, snapshot)
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
	// Own-chat observation is independent of grants. Baseline filenames only;
	// do not decode old conversations to discover the newly launched chat.
	historyAuthorized := watch.authorized
	watch.authorized = nil
	if watch.openCodeRun == nil {
		extension := ".jsonl"
		if watch.antigravity {
			extension = ".db"
		}
		existing := map[string]bool{}
		recovering := len(recovered) > 0 && recovered[0] != nil
		err := filepath.WalkDir(watch.dir, func(path string, entry os.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(path, extension) {
				id := strings.TrimSuffix(entry.Name(), extension)
				old := baseline[id] || baseline["file:"+path]
				for previous := range baseline {
					if strings.HasPrefix(entry.Name(), "rollout-") && strings.HasSuffix(entry.Name(), "-"+previous+extension) {
						old = true
					}
				}
				if !recovering || old {
					existing[path] = true
					baseline[id] = true
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		watch.seen = map[string]string{}
		boundPath := ""
		saved := loadChatBinding(root)
		if saved.SessionID == savedID && savedID != "" {
			boundPath = saved.HistoryPath
		}
		var candidatePath string
		watch.chatFile = func(path string) bool {
			w.tmuxMu.Lock()
			a := w.tmuxActiveWins["marshal-chat"]
			id := savedID
			if a != nil && a.sessionID != "" {
				id = a.sessionID
			}
			w.tmuxMu.Unlock()
			if boundPath != "" {
				if path != boundPath {
					return false
				}
				candidatePath = path
				return true
			}
			if id != "" && filepath.Base(path) != id+extension && !(strings.HasPrefix(filepath.Base(path), "rollout-") && strings.HasSuffix(path, "-"+id+extension)) {
				return false
			}
			if id == "" && existing[path] {
				return false
			}
			candidatePath = path
			return true
		}
		watch.observeSession = func(tr importer.SessionTranscript) error {
			if savedID != "" && tr.SessionID != savedID || savedID == "" && baseline[tr.SessionID] {
				return nil
			}
			w.tmuxMu.Lock()
			if a := w.tmuxActiveWins["marshal-chat"]; a != nil && (a.sessionID == "" || a.sessionID == tr.SessionID) {
				boundPath = candidatePath
				a.historyPath = boundPath
			}
			w.tmuxMu.Unlock()
			return w.captureChatConversation(root, tr.SessionID)
		}
	} else if len(recovered) == 0 || recovered[0] == nil {
		watch.observeSession = func(tr importer.SessionTranscript) error { baseline[tr.SessionID] = true; return nil }
		watch.consume = func(tr importer.SessionTranscript) error { baseline[tr.SessionID] = true; return nil }
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
	if watch.observeSession == nil || watch.chatFile == nil {
		watch.observeSession = observe
	}
	watch.consume = func(tr importer.SessionTranscript) error {
		if err := observe(tr); err != nil {
			return err
		}
		w.tmuxMu.Lock()
		active := w.tmuxActiveWins["marshal-chat"]
		bound := active != nil && active.sessionID != "" && active.sessionID == tr.SessionID
		w.tmuxMu.Unlock()
		if bound {
			w.observeAuthenticatedMarshalProposals(tr)
		}
		// Earlier or unbound conversations are never imported by this consumer.
		if bound && (historyAuthorized == nil || historyAuthorized(watch.dir)) {
			return previous(tr)
		}
		return nil
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
		history, _ := nativehistory.HistoryDir(nativehistory.Provider(provider), root)
		watch = newNativeHistoryWatch(history, root)
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

// joinedWorkers lists the panes joined into the Board window that /view
// focus and /view hide break out. The docked Marshal chat is not one of
// them: only exit and /view classic undock it.
func (w *Workspace) joinedWorkers() []*activeTmuxAgent {
	docked := w.dockedChat()
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	var joined []*activeTmuxAgent
	for _, a := range w.tmuxActiveWins {
		if a.isJoined && (docked == "" || a.paneID != docked) {
			joined = append(joined, copyAgentLocked(a))
		}
	}
	return joined
}

func (w *Workspace) breakJoinedPanes(ctx context.Context, joined []*activeTmuxAgent) error {
	for _, a := range joined {
		if err := tmux.BreakPane(ctx, a.paneID, a.window); err != nil {
			return err
		}
		w.tmuxMu.Lock()
		if current := w.tmuxActiveWins[a.id]; current != nil && current.paneID == a.paneID {
			current.isJoined = false
		}
		w.tmuxMu.Unlock()
	}
	return nil
}

func tmuxPaneSize(pane *string) workerterminal.Size {
	// The host publishes the immutable pane before Attach starts polling.
	return func(ctx context.Context) (uint16, uint16, error) {
		out, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", *pane, "#{pane_height} #{pane_width}")
		if err != nil {
			return 0, 0, err
		}
		var rows, cols uint16
		_, err = fmt.Sscanf(strings.TrimSpace(string(out)), "%d %d", &rows, &cols)
		return rows, cols, err
	}
}

// marshalDraftRejected retains consumed producer bytes and reports only a
// finite validation stage. Native draft contents and errors remain private.
func (w *Workspace) marshalDraftRejected(m *marshalSession, runID, provider, stage string, cause ...error) {
	switch stage {
	case "draft validation failed", "plan pack transfer failed", "plan pack validation failed", "canonical Process 04 evidence unavailable":
	default:
		return
	}
	m.mu.Lock()
	current := m.runID == runID
	m.mu.Unlock()
	if !current {
		return
	}
	note := "Marshal " + stage + "; consumed draft retained. Review it and any transferred plan pack before preparing a fresh handoff."
	if stage == "canonical Process 04 evidence unavailable" {
		note = "Marshal cannot offer plan approval until Process 04 verifies the current Goal, project context, and every selected worker profile. Inspect /harness status, verify the missing workers, then prepare a fresh draft. Consumed draft retained."
	}
	// The exact runtime reason is what lets the Marshal correct and
	// republish the draft. Draft validation errors can quote untrusted draft
	// content, so only runtime-side reasons are repeated.
	if stage != "draft validation failed" {
		for _, err := range cause {
			if err != nil {
				note += " Reason: " + RedactContent(err.Error(), nil)
				break
			}
		}
	}
	note += " The draft watcher remains active: publish a corrected draft to import it."
	w.marshalPublish(m, runID, w.marshalPanelWithNote(runID, provider, note))
	w.RecordActivity(note)
	if view, err := openInboxView(w.workDir, "marshal", false); err == nil {
		if view.write(fmt.Sprintf("## runtime · %s · draft rejected\n\n%s\nRun: %s\n", time.Now().UTC().Format(time.RFC3339), note, runID)) == nil {
			_ = view.trim()
		}
	}
}

// draftSupersedesRun reports whether a published draft must become a fresh
// run instead of the watched one. A draft only ever creates a run (it is
// stored at revision 0), so any stored run is superseded: one that left
// drafting (a returned governed task paused it for a new Process 04 plan),
// and also one still drafting whose plan can no longer be approved, for
// example after its Goal was revised. Reusing a stored run's ID failed with
// "marshal_runs revision 1, expected 0" and left the new draft unimported.
func draftSupersedesRun(snapshotErr error) bool {
	return snapshotErr == nil
}

// marshalDraftReady reports a validated, published draft waiting for import.
func marshalDraftReady(root string) bool {
	path := filepath.Join(root, marshalDraftRelativePath)
	if _, err := os.Stat(path + ".ready"); err != nil {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// cancelSupersededTaskLanes cancels unfinished task lanes left by an earlier
// plan once a new plan replaced it. Their file-scope leases would otherwise
// refuse the new plan's lanes ("task lane scope conflicts").
func (w *Workspace) cancelSupersededTaskLanes(ctx context.Context, planID string) {
	if w.runtime == nil || w.runtime.Execution() == nil || w.runtime.Execution().Engine() == nil || planID == "" {
		return
	}
	runs, err := w.runtime.Execution().ListRuns(ctx)
	if err != nil {
		return
	}
	parents := make(map[string]string, len(runs))
	for _, run := range runs {
		parents[run.RunID] = run.PlanID
	}
	for _, lane := range runs {
		if lane.ParentRunID == "" || lane.State.IsTerminal() || lane.State == execution.RunDonePendingVerification {
			continue
		}
		if plan := parents[lane.ParentRunID]; plan == "" || plan == planID {
			continue
		}
		if _, err := w.runtime.Execution().Engine().ControlRun(ctx, lane.RunID, "cancel", lane.Version); err == nil {
			w.RecordActivity("Cancelled task lane " + lane.RunID + " of a superseded plan")
		}
	}
}
