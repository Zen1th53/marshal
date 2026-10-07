package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/tmux"
	"io"
	"maps"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/collaboration"
	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

// Workspace encapsulates the live Terminal TUI Control Plane over canonical runtime state.
type Workspace struct {
	workers            workerLifecycle
	permissions        permissionState
	governedDispatches map[string]context.CancelFunc
	governedDispatchWG sync.WaitGroup

	egressGate ioGate
	mu         sync.RWMutex
	store      *store.Store
	coord      *collaboration.Coordinator
	router     *harness.ULTRARouter
	projectID  string
	sessionID  string
	workDir    string
	mode       string
	state      UIState
	cmd        *CommandHandler
	theme      *Theme
	composer   *Composer
	completer  *Completer
	palette    *CommandPalette
	diffViewer *DiffViewer
	// navView is the frozen-IA navigation surface. It owns the screen while
	// open, so MARSHAL is operable without knowing a single slash command.
	navView *NavView
	// navErr is why the navigation view is missing, if it is.
	navErr error

	// runtime is the canonical mutation authority Control submits through.
	// It is nil in a store-only workspace, in which case every Control action
	// refuses with that reason rather than writing anything locally.
	runtime *app.Runtime
	// projectIdentity is the canonical project id the plan and execution
	// services are keyed by.
	projectIdentity projectid.ID
	// runtimeReplaced is set only after a stopped-runtime restore has reopened
	// the canonical runtime. The input loop reattaches its read/mutation
	// sources after the current confirmation releases NavView's lock.
	runtimeReplaced bool
	controlOverride *ControlSource
	// ultraRequest asks the Community Cloud for an entitlement through the
	// canonical client. Nil when no Cloud is configured.
	ultraRequest func(ctx context.Context) error
	terminal     *Terminal
	out          io.Writer

	// ultra is the canonical ULTRA authorization gate, shared with every other
	// entry path. It is nil until a Cloud session is attached, and a nil gate
	// answers "not entitled", so the TUI's default remains Standard.
	ultra *cloud.Gate
	// marshal holds the active Marshal run and the approvals the person gave
	// for it in this session. Guarded by mu for creation.
	marshal *marshalSession
	// ultraExecution is the user's ULTRA Execution preference. It is not an
	// authority: with no entitlement it changes nothing.
	ultraExecution bool
	// navReleased reports whether the navigation surface may be opened at
	// all; it is taken from navigationReleased when the workspace is built.
	navReleased bool
	// ultraStopAsked records that /ultra stop asked the person to confirm;
	// only /ultra stop confirm straight after it turns execution off.
	ultraStopAsked bool
	// ultraClient and ultraState are what asking for an entitlement needs: the
	// request is signed with the installation key, so the gate alone is not
	// enough. Nil when the Cloud is unconfigured, which is why every use
	// checks first.
	ultraClient  *cloud.Client
	ultraState   cloud.State
	ultraSession string
	// ultraErr is why authorization did not produce ULTRA, if it did not.
	// Without it "unavailable" covers both "you were never entitled" and "the
	// server said 429", which are different problems with different answers —
	// and the second one leaves a user retyping a password that was never
	// wrong.
	ultraErr error

	// screen owns in-place frame painting. Without it every redraw appended to
	// scrollback, so each keystroke left another prompt banner behind.
	screen *Screen

	// Completion popup state. Tab is completion only: it opens or cycles this
	// list and never submits, so it can never execute a partially typed command.
	// lastRoute is the most recent /route request and result, bound to the
	// goal revision it was computed for, so /why explains exactly that route.
	lastRoute *routeRecord

	completionOpen     bool
	completionSelected bool
	completionText     string
	// mouseOn tracks whether mouse reporting is currently held, so the mode is
	// only written to the terminal when it actually changes.
	mouseOn         bool
	completions     []string
	completionIndex int
	// completionCycled records that Tab has already put a candidate on the
	// line, so the first press takes the highlighted one instead of the next.
	completionCycled bool
	completionStem   string

	// interruptArmed records that a Ctrl+C arrived with nothing left to
	// interrupt. A second consecutive press then exits; any other key disarms
	// it, so a stray interrupt never closes the workspace on its own.
	interruptArmed        bool
	exitRequested         bool
	nativeOnStart         *[]string
	nativeStartupProvider string
	// nativeProvider records which agent a session last opened. It is an
	// observation of what happened, not a routing decision: nothing is dispatched
	// to "the last provider", because launching an agent is always something the
	// operator asked for by name.
	nativeProvider string

	tmuxPath          string
	tmuxSession       string
	tmuxMarshalWin    string
	tmuxMarshalWinID  string
	tmuxMarshalPaneID string
	tmuxFollowActive  bool
	tmuxAlerts        map[string]string
	tmuxDelivered     map[string]bool
	tmuxActiveWins    map[string]*activeTmuxAgent
	tmuxMu            sync.Mutex
	tmuxStatusBusy    atomic.Bool
	tmuxStatusDirty   atomic.Bool
	tmuxMonitors      sync.WaitGroup

	// Scroll and activity unread tracking
	uiEvents              chan func()
	repaint               chan struct{}
	commandBusy           atomic.Bool
	navigationOpening     atomic.Bool
	commandWG             sync.WaitGroup
	commandCancelMu       sync.Mutex
	commandCancel         context.CancelFunc
	completionProbeBusy   atomic.Bool
	completionProbeTime   time.Time
	completionSkillsReady bool
	commandNames          []string

	scrollOffset int
	unreadNew    int
}

// AttachULTRA wires the canonical ULTRA gate into the workspace.
//
// The TUI does not decide entitlement for itself; it asks the same gate the CLI
// asks. That is what makes "every entry path is gated" a property of the code
// rather than a convention someone has to remember.
func (w *Workspace) AttachULTRA(gate *cloud.Gate, executionEnabled bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ultra = gate
	w.ultraExecution = executionEnabled
	w.state.UltraEntitled = gate.Entitled()
	w.state.UltraExecution = executionEnabled
}

// AttachULTRARequester supplies what asking for an entitlement needs.
//
// Separate from AttachULTRA because a session can hold a verified gate without
// being able to ask for anything — a client that already has ULTRA has nothing
// to request — and because the request path needs the installation key, which
// the gate deliberately does not carry.
func (w *Workspace) AttachULTRARequester(client *cloud.Client, state cloud.State, sessionID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ultraClient = client
	w.ultraState = state
	w.ultraSession = sessionID
	if client == nil {
		w.ultraRequest = nil
		return
	}
	// Control and the slash-command surface share the same proof-of-possession
	// client and installation state. The returned status is deliberately not
	// converted into entitlement: only a subsequently verified signed lease
	// can change the gate's answer.
	w.ultraRequest = func(ctx context.Context) error {
		_, err := client.RequestEntitlement(ctx, state, sessionID)
		return err
	}
}

// AttachULTRAError records why activation failed, so /ultra can say.
func (w *Workspace) AttachULTRAError(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ultraErr = err
}

// ultraError returns the activation failure, if there was one.
func (w *Workspace) ultraError() error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.ultraErr
}

// ultraRequester returns what is needed to ask for an entitlement.
func (w *Workspace) ultraRequester() (*cloud.Client, cloud.State, string) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.ultraClient, w.ultraState, w.ultraSession
}

// ultraGate returns the gate under the workspace lock.
func (w *Workspace) ultraGate() (*cloud.Gate, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.ultra, w.ultraExecution
}

// setUltraExecution records the person's ULTRA Execution preference and
// withdraws any pending stop question.
func (w *Workspace) setUltraExecution(enabled bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ultraExecution = enabled
	w.ultraStopAsked = false
}

// askUltraStop records that the person was asked to confirm /ultra stop.
func (w *Workspace) askUltraStop(asked bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ultraStopAsked = asked
}

// takeUltraStop reports whether a stop question is pending and withdraws it,
// so one question allows one confirmation.
func (w *Workspace) takeUltraStop() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	asked := w.ultraStopAsked
	w.ultraStopAsked = false
	return asked
}

// NewWorkspace instantiates a dynamic terminal workspace connected to the canonical store.
func NewWorkspace(st *store.Store, projectID, sessionID string) *Workspace {
	if sessionID == "" {
		sessionID = fmt.Sprintf("tui-session-%d", time.Now().UnixNano())
	}
	if projectID == "" {
		projectID = "marshal"
	}

	cwd, _ := os.Getwd()
	th := NewTheme(ThemeDefault, true, true)

	participants := DiscoverTeamParticipants(nil)
	agentIDs := make([]string, 0, len(participants))
	for _, p := range participants {
		agentIDs = append(agentIDs, p.AgentID)
	}
	hasCodex := false
	for _, id := range agentIDs {
		if id == "codex" {
			hasCodex = true
			break
		}
	}
	if !hasCodex {
		agentIDs = append(agentIDs, "codex")
	}

	compCtx := CompletionContext{
		Commands: []string{
			"/status", "/goal", "/mode", "/claims", "/inspect", "/approve", "/reject",
			"/route", "/agents", "/evidence", "/why", "/msg", "/handoff", "/checkpoint",
			"/rollback", "/budget", "/pause", "/resume", "/cancel", "/doctor", "/tasks", "/task",
			"/policy", "/sandbox", "/memory", "/provider", "/providers", "/harness", "/model", "/models",
			"/effort", "/ultra", "/marshal", "/backup", "/fingerprint", "/runtime", "/store", "/export",
			"/reinjection", "/alignment", "/optimization", "/verification", "/diff", "/review",
			"/codex", "/claude", "/opencode", "/agy", "/antigravity", "/mcp", "/plugin", "/plugins", "/apply", "/sessions", "/fork",
			"/permission", "/continue", "/egress", "/roster", "/say", "/learning", "/memory-search", "/memory-stale", "/provenance", "/trust", "/fingerprints", "/playbooks", "/replay-index", "/approvals", "/approval", "/termination", "/context", "/update", "/?", "/exit",
			"/search", "/features", "/skill", "/skills", "/login", "/logout", "/help", "/quit",
			"/view", "/focus", "/takeover", "/take-over", "/stop",
		},
		Agents:      agentIDs,
		Subcommands: make(map[string][]string),
	}
	compCtx.Subcommands["/view"] = []string{"focus", "side-by-side", "worker", "show", "hide", "follow", "readonly", "takeover"}
	compCtx.Subcommands["/view show"] = []string{"codex", "claude", "opencode", "agy", "marshal"}
	compCtx.Subcommands["/stop"] = []string{"all", "workers"}
	compCtx.Subcommands["/store"] = []string{"check", "counts"}
	compCtx.Subcommands["/store check"] = []string{"quick", "full"}
	compCtx.Subcommands["/mode"] = []string{"manual", "auto", "ultra"}
	compCtx.Subcommands["/inspect"] = []string{"claim", "evidence", "checkpoint", "task", "handoff", "approval", "agent"}
	compCtx.Subcommands["/diff"] = []string{"staged", "unstaged", "untracked"}
	compCtx.Subcommands["/goal"] = []string{"create", "edit", "diff", "version", "criteria", "constraints", "add-constraint", "rm-constraint", "donotdo", "progress"}
	compCtx.Subcommands["/tasks"] = []string{"list", "create", "inspect", "assign", "pause", "resume", "cancel", "retry", "ownership"}
	compCtx.Subcommands["/task"] = []string{"list", "create", "inspect", "assign", "pause", "resume", "cancel", "retry", "ownership"}
	compCtx.Subcommands["/policy"] = []string{"network", "sandbox", "capability", "scope", "write", "audit"}
	compCtx.Subcommands["/checkpoint"] = []string{"list", "create", "inspect", "diff"}
	compCtx.Subcommands["/memory"] = []string{"list", "search", "provenance", "inject", "peers", "review", "request", "allow", "deny"}
	// Second level: the agents a channel line can name, plus the keywords.
	// Offer both the short agent command and the canonical provider name.
	compCtx.Subcommands["/memory peers"] = []string{"participants", "claude", "codex", "opencode", "agy", "antigravity"}
	compCtx.Subcommands["/memory inject"] = []string{"auto", "system-prompt", "project-doc", "prompt", "off", "preview", "clear"}
	compCtx.Subcommands["/memory inject preview"] = []string{"claude", "codex", "opencode", "agy", "antigravity"}
	compCtx.Subcommands["/ultra stop"] = []string{"confirm"}
	compCtx.Subcommands["/harness"] = []string{"probe", "status", "select"}
	compCtx.Subcommands["/ultra"] = []string{"status", "start", "stop", "request"}
	compCtx.Subcommands["/provider"] = []string{"status", "use", "config"}
	compCtx.Subcommands["/providers"] = compCtx.Subcommands["/provider"]
	compCtx.Subcommands["/alignment"] = []string{"scope", "violations", "blast", "deletions", "resolve", "status"}
	compCtx.Subcommands["/codex"] = providerSubcommands["codex"]
	compCtx.Subcommands["/mcp"] = []string{"list", "add", "get", "remove", "rm", "delete"}
	compCtx.Subcommands["/claude"] = providerSubcommands["claude"]
	compCtx.Subcommands["/opencode"] = providerSubcommands["opencode"]
	compCtx.Subcommands["/agy"] = providerSubcommands["agy"]
	compCtx.Subcommands["/antigravity"] = compCtx.Subcommands["/agy"]
	compCtx.Subcommands["/marshal"] = marshalSubcommands
	compCtx.Subcommands["/marshal model"] = []string{"codex", "claude", "agy"}
	compCtx.Subcommands["/marshal amend"] = []string{"approve", "deny"}
	compCtx.Subcommands["/marshal settings"] = []string{"execution-rights", "acceptance-mode", "rework-limit", "ultra-concurrency", "control"}
	compCtx.Subcommands["/marshal settings execution-rights"] = []string{"none", "read-only", "small-tasks"}
	compCtx.Subcommands["/marshal settings acceptance-mode"] = []string{"marshal", "marshal-then-user", "user"}
	compCtx.Subcommands["/marshal settings control"] = []string{"free", "strict"}
	for _, reader := range []string{"participants", "claude", "codex", "opencode", "agy", "antigravity"} {
		compCtx.Subcommands["/memory peers "+reader] = []string{"all", "none", "claude", "codex", "opencode", "agy", "antigravity"}
		if reader == "participants" {
			compCtx.Subcommands["/memory peers "+reader] = []string{"all", "claude", "codex", "opencode", "agy", "antigravity"}
		}
	}
	compCtx.Subcommands["/plugin"] = []string{"list", "add", "install", "remove", "rm", "uninstall", "marketplace"}
	compCtx.Subcommands["/plugins"] = []string{"list", "add", "install", "remove", "rm", "uninstall", "marketplace"}
	compCtx.Subcommands["/codex mcp"] = compCtx.Subcommands["/mcp"]
	compCtx.Subcommands["/codex plugin"] = compCtx.Subcommands["/plugin"]
	compCtx.Subcommands["/codex plugins"] = compCtx.Subcommands["/plugin"]
	compCtx.Subcommands["/skill"] = []string{"install"}
	compCtx.Subcommands["/codex skill"] = compCtx.Subcommands["/skill"]
	compCtx.Subcommands["/codex features"] = []string{"list", "enable", "disable"}
	compCtx.Subcommands["/backup"] = []string{"create", "restore"}
	compCtx.Subcommands["/model"] = []string{"show", "select"}
	compCtx.Subcommands["/provider use"] = []string{"codex", "claude", "opencode", "agy"}
	compCtx.Subcommands["/models"] = compCtx.Subcommands["/provider use"]
	compCtx.Subcommands["/effort"] = []string{"minimal", "low", "medium", "high", "xhigh", "default"}
	compCtx.Subcommands["/features"] = []string{"list", "enable", "disable"}
	compCtx.Subcommands["/doctor"] = []string{"codex", "provider"}
	compCtx.Subcommands["/search"] = []string{"on", "off", "enable", "disable", "true", "false"}
	compCtx.Subcommands["/sandbox"] = []string{"read-only", "workspace-write"}
	compCtx.Subcommands["/resume"] = []string{"--last"}
	compCtx.Subcommands["/fork"] = []string{"--last"}
	compCtx.Subcommands["/codex resume"] = compCtx.Subcommands["/resume"]
	compCtx.Subcommands["/codex fork"] = compCtx.Subcommands["/fork"]
	compCtx.Subcommands["/codex sandbox"] = compCtx.Subcommands["/sandbox"]
	compCtx.Subcommands["/codex approval"] = []string{"on-request", "never"}
	compCtx.Subcommands["/codex search"] = compCtx.Subcommands["/search"]

	qualifyProviderCompletions(context.Background(), &compCtx, false)
	completer := NewCompleter(compCtx)
	composer := NewComposer(th)
	composer.SetPrompt(ComposerPromptInfo{
		Project: projectID,
		Mode:    "MANUAL",
		State:   "NO_GOAL",
		Agent:   "",
	})

	paletteActions := GlobalRegistry.ToPaletteActions()
	palette := NewCommandPalette(th, paletteActions)
	diffViewer := NewDiffViewer(th, cwd)

	// The frozen-IA navigation view. If the embedded manifest will not load the
	// workspace still opens: losing navigation must not cost somebody their
	// session. The error is kept rather than discarded so that pressing Ctrl+N
	// reports what actually failed instead of a bare "unavailable".
	navView, navErr := NewNavView(th)

	ws := &Workspace{
		commandNames: append([]string(nil), compCtx.Commands...),
		repaint:      make(chan struct{}, 1),
		store:        st,
		coord:        collaboration.NewCoordinator(st, nil),
		router:       harness.NewULTRARouter(nil),
		projectID:    projectID,
		sessionID:    sessionID,
		workDir:      cwd,
		mode:         "manual",
		theme:        th,
		composer:     composer,
		completer:    completer,
		palette:      palette,
		diffViewer:   diffViewer,
		navView:      navView,
		navErr:       navErr,
		terminal:     NewTerminal(os.Stdin, os.Stdout),
		state: UIState{
			ProjectID:          projectID,
			SessionID:          sessionID,
			SessionMode:        "MANUAL",
			UnderstandingState: model.GoalNeedsInput,
			GitStatus:          ProbeGitStatus(cwd),
			Participants:       participants,
		},
	}
	ws.completer.ctx.InstallableSkills = ws.readInstallableSkills
	ws.navReleased = navigationReleased
	ws.cmd = NewCommandHandler(ws)
	ws.tmuxActiveWins = make(map[string]*activeTmuxAgent)
	return ws
}

// SetTheme configures the active theme mode and animation setting.
func (w *Workspace) SetTheme(mode ThemeMode, animation bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	unicode := true
	if mode == ThemeNoColor {
		unicode = false
	}
	w.theme = NewTheme(mode, unicode, animation)
	w.composer.theme = w.theme
	w.palette.theme = w.theme
	w.diffViewer.theme = w.theme
	w.navView.SetTheme(w.theme)
}

// SetCoordinator sets an explicit coordinator.
func (w *Workspace) SetCoordinator(c *collaboration.Coordinator) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.coord = c
}

// SetRouter sets an explicit ULTRARouter.
func (w *Workspace) SetRouter(r *harness.ULTRARouter) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.router = r
}

// RefreshState pulls current ground truth from canonical SQLite tables and live probes.
func (w *Workspace) RefreshState(ctx context.Context) error {
	w.mu.RLock()
	state := w.state
	state.Participants = append([]model.Participant(nil), state.Participants...)
	previousMessages := len(state.RecentMessages)
	st, workDir, mode := w.store, w.workDir, w.mode
	w.mu.RUnlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	defer func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.state.GitStatus = state.GitStatus
		w.state.Participants = state.Participants
		w.state.Goal = state.Goal
		w.state.Claims = state.Claims
		w.state.UnderstandingState = state.UnderstandingState
		w.state.BudgetConsumed = state.BudgetConsumed
		w.state.TerminationState = state.TerminationState
		w.state.ActiveTurn = state.ActiveTurn
		w.state.RecentMessages = state.RecentMessages
		w.state.PendingApprovals = state.PendingApprovals
	}()

	// 1. Live Git status
	state.GitStatus = ProbeGitStatus(workDir)

	// 2. Reconcile participants with real live probes (honest state)
	state.Participants = DiscoverTeamParticipants(state.Participants)

	if st == nil {
		return nil
	}

	// 3. Recover active GoalContract for this session
	goal, err := st.GetActiveGoalContract(ctx, w.sessionID)
	if err != nil && !errors.Is(err, model.ErrGoalNotFound) {
		return fmt.Errorf("read active goal: %w; reopen the TUI and check /store", err)
	}
	if err == nil {
		state.Goal = goal
		state.UnderstandingState = goal.UnderstandingState

		// 4. Recover Claims for this goal
		claims, err := st.ListClaimsByGoal(ctx, goal.ID, goal.Revision)
		if err == nil {
			state.Claims = claims
		}

		// 5. Recover Budget for this goal
		budget, err := st.GetBudgetTracker(ctx, w.sessionID, goal.ID, goal.Revision)
		if err == nil && budget != nil {
			state.BudgetConsumed = *budget
		} else if errors.Is(err, model.ErrNotFound) {
			state.BudgetConsumed = model.ConsumedBudget{}
		}

		// 6. Recover Termination status if any
		term, err := st.GetGoalTermination(ctx, w.sessionID, goal.ID, goal.Revision)
		if err == nil && term != nil {
			state.TerminationState = term.State
		} else if errors.Is(err, model.ErrNotFound) {
			state.TerminationState = ""
		}
	}

	// 7. Recover Collaborative Session if existing
	sess, err := st.GetTeamSession(ctx, w.sessionID)
	if err == nil && sess != nil {
		state.Participants = DiscoverTeamParticipants(sess.Participants)
		state.ActiveTurn = sess.ActiveTurn
	}

	// 8. Recover recent messages
	msgs, err := st.ListAgentMessages(ctx, w.sessionID, 30)
	if err == nil {
		state.RecentMessages = msgs
	}

	// 8b. Recover pending approvals
	pending, err := st.ListPendingApprovals(ctx, state.ProjectID)
	if err == nil {
		state.PendingApprovals = pending
	}

	// 9. Update autocomplete context with live objects
	var claimIDs []string
	var evidenceIDs []string
	for _, c := range state.Claims {
		claimIDs = append(claimIDs, c.ID)
		for _, ev := range append(append([]model.EvidenceRef{}, c.SupportingEvidence...), c.ContradictingEvidence...) {
			evidenceIDs = append(evidenceIDs, ev.EvidenceID)
		}
	}
	var agentIDs []string
	for _, p := range state.Participants {
		agentIDs = append(agentIDs, p.AgentID)
	}

	tasks, _ := st.ListTasks(ctx)
	var taskIDs []string
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
	}

	var cpIDs []string
	if st != nil {
		checkpoints, _ := st.ListHandoffCheckpoints(ctx, "task-interactive")
		for _, cp := range checkpoints {
			cpIDs = append(cpIDs, cp.ID)
		}
	}

	w.postUI(ctx, func() {
		if w.scrollOffset > 0 && len(state.RecentMessages) > previousMessages {
			w.unreadNew += len(state.RecentMessages) - previousMessages
		}
		compCtx := w.completer.ctx
		compCtx.Claims, compCtx.Evidence, compCtx.Agents = claimIDs, evidenceIDs, agentIDs
		compCtx.Tasks, compCtx.Checkpoints = taskIDs, cpIDs
		w.completer.UpdateContext(compCtx)
		w.composer.SetPrompt(ComposerPromptInfo{Project: w.projectID, Mode: strings.ToUpper(mode), State: string(state.UnderstandingState)})
	})

	return nil
}

// GetUIState returns a snapshot copy of current UI state.
// liveStateLocked overlays the live ULTRA gate on the cached state, so every
// surface derives entitlement, navigation and the effective mode label from
// the gate at render time rather than from a value captured earlier. Callers
// hold w.mu.
func (w *Workspace) liveStateLocked() UIState {
	state := w.state
	state.UltraEntitled = w.ultra.Entitled()
	state.UltraExecution = w.ultraExecution
	state.NavigationAvailable = w.navReleased && state.UltraEntitled
	state.SessionMode = effectiveModeLabel(w.mode, state.UltraEntitled)
	return state
}

// effectiveModeLabel names the supervision mode as it currently applies. An
// ULTRA preference without a live entitlement is shown as inactive instead of
// as ULTRA, so a withdrawn or expired lease never leaves an ULTRA label behind.
func effectiveModeLabel(mode string, entitled bool) string {
	label := strings.ToUpper(mode)
	if label == "" {
		label = "MANUAL"
	}
	if label == "ULTRA" && !entitled {
		return "ULTRA (INACTIVE: no verified entitlement)"
	}
	return label
}

func (w *Workspace) GetUIState() UIState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	state := w.liveStateLocked()
	return state
}

// ExecuteCommand executes an interactive slash command.
func (w *Workspace) ExecuteCommand(ctx context.Context, line string) (string, error) {
	res, err := w.cmd.Handle(ctx, line)
	if err != nil {
		return res, err
	}
	_ = w.RefreshState(ctx)
	w.replayWorkerAlerts(ctx)
	return res, nil
}

// Run starts the interactive terminal loop.
// Supports full raw mode line editing, Tab autocomplete, Ctrl+P palette, diff viewer,
// or clean fallback to buffered scanner if non-terminal.
func (w *Workspace) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	defer w.Close()
	w.out = out
	if w.terminal != nil && w.terminal.IsTerminal() && in == os.Stdin && out == os.Stdout {
		w.uiEvents = make(chan func(), 64)
	}
	if tmux.IsInsideTmux() {
		w.InitTmux()
	}

	_ = w.RefreshState(ctx)
	w.replayWorkerAlerts(ctx)

	// The check is a read of a public feed and installs nothing. It runs off
	// this path so a slow or unreachable feed cannot delay the workspace.
	w.checkForUpdateInBackground(ctx)

	// Check if running in a real interactive terminal
	if w.terminal != nil && w.terminal.IsTerminal() && in == os.Stdin && out == os.Stdout {
		return w.runRawTerminal(ctx)
	}

	// Fallback for piped or non-terminal environments
	if w.nativeOnStart != nil {
		return fmt.Errorf("native agent requires an interactive terminal")
	}
	return w.runLineScanner(ctx, in, out)
}

func (w *Workspace) runRawTerminal(ctx context.Context) error {
	releaseOutput, err := w.terminal.boundOutput()
	if err != nil {
		return err
	}
	defer releaseOutput()
	if w.out == nil {
		w.out = os.Stdout
	}
	if err := w.terminal.MakeRaw(); err != nil {
		return w.runLineScanner(ctx, os.Stdin, os.Stdout)
	}

	w.out = w.terminal.out

	// Run on the alternate screen so the workspace never disturbs the shell's
	// scrollback, and unwind it in reverse on every exit path, including a
	// panic, so a crash cannot strand the terminal in raw mode.
	w.terminal.EnterAltScreen()
	w.terminal.EnableBracketedPaste()
	w.syncMouseMode()
	w.screen = NewScreen(w.terminal)
	defer func() {
		w.terminal.DisableMouse()
		w.terminal.DisableBracketedPaste()
		w.terminal.ShowCursor()
		w.terminal.LeaveAltScreen()
		w.terminal.Restore()
		w.reportActiveTmuxSessions()
		fmt.Fprintln(w.out, "Exiting MARSHAL terminal workspace. Any durable session data is preserved.")
	}()

	loopCtx, loopCancel := context.WithCancel(ctx)
	ctx = loopCtx
	defer func() { loopCancel(); w.Close() }()
	w.navView.OnRepaint(w.requestRepaint)
	w.navView.mu.Lock()
	w.navView.asyncActions = true
	w.navView.commandBusy = &w.commandBusy
	w.navView.mu.Unlock()
	w.renderFullView()
	if w.nativeOnStart != nil {
		args := *w.nativeOnStart
		w.nativeOnStart = nil
		provider := w.nativeStartupProvider
		if provider == "" {
			provider = "codex"
		}
		result, err := w.runNativeAgent(ctx, provider, args)
		w.state.LastCommand = "native " + provider
		w.state.LastOutput = result
		if err != nil {
			w.state.LastOutput += "\n" + err.Error()
			w.state.LastOutputIsError = true
		}
		w.renderFullView()
	}

	// Read keys on their own goroutine. ReadKey blocks on the terminal, so
	// polling it from the select's default branch pinned the loop inside that
	// branch and starved SIGWINCH: a resize only repainted once the operator
	// happened to press a key. Feeding keys through a channel makes input and
	// resize equal event sources, and the select blocks rather than spinning.
	type keyRead struct {
		event KeyEvent
		err   error
	}
	keys := make(chan keyRead)
	// Acknowledge processing before reading again: an interactive child must
	// own stdin exclusively until its command handler returns.
	readNext := make(chan struct{})
	readerDone := make(chan struct{})
	readerCtx, readerCancel := context.WithCancel(ctx)
	readerStopped := make(chan struct{})
	defer func() { close(readerDone); readerCancel(); <-readerStopped }()

	go func() {
		defer close(readerStopped)
		for {
			ev, err := w.terminal.ReadKeyContext(readerCtx)
			select {
			case keys <- keyRead{ev, err}:
			case <-readerDone:
				return
			}
			if err != nil {
				return
			}
			select {
			case <-readNext:
			case <-readerDone:
				return
			}
		}
	}()

	keyPending := false
	for {
		if keyPending {
			select {
			case readNext <- struct{}{}:
			case <-ctx.Done():
				return nil
			}
			keyPending = false
		}
		select {
		case <-ctx.Done():
			return nil
		case fn := <-w.uiEvents:
			fn()
			if w.commandExitRequested() {
				return nil
			}
			w.renderFullView()
		case <-w.repaint:
			w.renderFullView()
		case <-w.terminal.ResizeEvents():
			// A resize invalidates the diff baseline: the previous frame was
			// laid out for the old geometry.
			w.screen.Reset()
			w.renderFullView()
		case kr := <-keys:
			keyPending = kr.err == nil
			event, err := kr.event, kr.err
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return fmt.Errorf("read terminal input: %w", err)
			}

			// Ctrl+C is an interrupt, not a quit. It unwinds the innermost
			// context first: an open overlay, then pending composer input. Only
			// when there is nothing left to interrupt does a second consecutive
			// press exit, so a reflexive Ctrl+C never discards a session the
			// operator is still working in.
			if event.Type == KeyCtrlC {
				if w.commandBusy.Load() {
					w.cancelCommand()
					continue
				}
				if w.interruptNavigation() {
					w.renderFullView()
					continue
				}
				if w.palette.IsOpen() {
					w.palette.Close()
					w.interruptArmed = false
					w.renderFullView()
					continue
				}
				if w.diffViewer.IsOpen() {
					w.diffViewer.Close()
					w.interruptArmed = false
					w.renderFullView()
					continue
				}
				if w.composer.Text() != "" {
					w.composer.SetText("")
					w.interruptArmed = false
					w.renderFullView()
					continue
				}
				if !w.interruptArmed {
					w.interruptArmed = true
					w.renderFullView()
					fmt.Fprintln(w.out, "Press Ctrl+C again to exit, or /quit. Any durable session data is preserved either way.")
					continue
				}
				w.terminal.ClearScreen()
				return nil
			}

			// Any other key cancels a pending exit confirmation.
			w.interruptArmed = false

			// Navigation owns every key while it is open, and Ctrl+N opens it.
			if event.Type == KeyF7 {
				w.runCommand(ctx, "/codex")
				continue
			}
			if event.Type == KeyF8 {
				w.runCommand(ctx, "/claude")
				continue
			}
			if event.Type == KeyF9 {
				w.runCommand(ctx, "/opencode")
				continue
			}
			// F10 is the update action next to the notice. With a release
			// already found it installs that release; with none found it is
			// the check, so the key means the same thing either way: ask
			// about the update.
			if event.Type == KeyF10 {
				w.runCommand(ctx, w.updateKeyCommand())
				continue
			}
			if event.Type == KeyF11 {
				// F11 returns to MARSHAL from tmux agent windows; inside MARSHAL
				// it leaves the workspace and any in-progress composer draft alone.
				continue
			}
			if event.Type == KeyF12 {
				w.runCommand(ctx, "/agy")
				continue
			}
			if event.Type == KeyCtrlX {
				// One key stops all workers but never the Marshal (Decision 9)
				w.runCommand(ctx, "/stop all")
				continue
			}
			// The dispatch lives in its own method so a test can drive exactly
			// the branch this loop takes rather than a copy of it.
			if w.dispatchNavigationKey(ctx, event) {
				w.syncMouseMode()
				w.mu.RLock()
				exitRequested := w.exitRequested
				w.mu.RUnlock()
				if exitRequested {
					w.terminal.ClearScreen()
					return nil
				}
				w.renderFullView()
				continue
			}

			// Handle Command Palette (Ctrl+P)
			if event.Type == KeyCtrlP {
				w.closeCompletion()
				w.palette.Toggle()
				w.renderFullView()
				continue
			}

			if w.palette.IsOpen() {
				action, executed := w.palette.HandleKey(event)
				if executed && action != nil {
					w.runCommand(ctx, action.Command)
				}
				w.renderFullView()
				continue
			}

			// Handle Diff Viewer (d / Esc)
			if w.diffViewer.IsOpen() {
				if w.diffViewer.HandleKey(event) {
					w.renderFullView()
					continue
				}
			}

			// Function keys (F1–F6) provide immediate one-touch actions without typing
			switch event.Type {
			case KeyF1:
				w.runCommand(ctx, "/help")
				continue
			case KeyF2:
				w.runCommand(ctx, "/review")
				continue
			case KeyF3:
				w.runCommand(ctx, "/diff")
				continue
			case KeyF4:
				w.runCommand(ctx, "/status")
				continue
			case KeyF5:
				w.runCommand(ctx, "/models")
				continue
			case KeyF6:
				w.runCommand(ctx, "/mcp")
				continue
			}

			// Esc toggles navigation mode when composer is empty and no popup/overlay is active
			if event.Type == KeyEsc && w.composer.Text() == "" && !w.completionOpen && !w.diffViewer.IsOpen() && !w.palette.IsOpen() {
				// Navigation is closed until verified, and an ULTRA surface
				// after that, so Esc on an empty composer must not drop a
				// session into it.
				if refusal := w.navigationRefusal(); refusal != "" {
					w.setOutput(refusal, false)
					continue
				}
				w.openNavigation(ctx)
				w.renderFullView()
				continue
			}

			// While the completion popup is open it owns the arrow keys, Tab,
			// Enter and Esc, so selection is never confused with history
			// navigation or submit. Moving the highlight leaves the buffer
			// alone; only accepting writes to it.
			if w.completionOpen {
				switch event.Type {
				case KeyEsc:
					w.closeCompletion()
					w.renderComposer()
					continue
				case KeyUp:
					w.moveCompletion(-1)
					w.renderComposer()
					continue
				case KeyDown:
					w.moveCompletion(1)
					w.renderComposer()
					continue
				case KeyShiftTab:
					w.cycleCompletion(-1)
					w.renderComposer()
					continue
				case KeyTab:
					// Tab steps to the next candidate and leaves the menu open,
					// the way a shell does. It never submits: Enter settles the
					// choice, and the operator can still add arguments first.
					w.cycleCompletion(1)
					w.renderComposer()
					continue
				case KeyEnter:
					// Enter settles the candidate being completed and stops
					// there, so the operator can Tab again for the next level
					// rather than running something half-written. The second
					// Enter runs it through the ordinary path, because by then
					// no menu is open.
					//
					// Nothing is being completed when the word at the cursor is
					// empty: the menu is only offering what could come next.
					// Without an explicit selection, Enter means what it
					// always means. Without this, "/goal " — a finished command
					// with a trailing space — would take a subcommand instead
					// of running, and the operator would watch Enter not work.
					if w.completionShouldSubmit() {
						w.closeCompletion()
						cmd, submitted := w.composer.HandleKey(KeyEvent{Type: KeyEnter})
						if submitted {
							if cmd == "/quit" || cmd == "/exit" {
								return nil
							}
							w.runCommand(ctx, cmd)
							if w.commandExitRequested() {
								w.terminal.ClearScreen()
								return nil
							}
						}
						w.renderComposer()
						continue
					}
					w.acceptCompletion()
					w.renderComposer()
					continue
				}
			}

			// Tab with no menu open offers one. An empty composer gets the full
			// command list, which is how the surface is discovered without
			// knowing a single command name already.
			if event.Type == KeyTab || event.Type == KeyShiftTab {
				if w.composer.Text() == "" {
					w.composer.SetText("/")
					w.composer.cursor = 1
				}
				w.refreshCompletion()
				w.renderComposer()
				continue
			}

			// Scroll navigation for transcript activity
			if event.Type == KeyPgUp {
				w.scrollOffset += 5
				w.renderFullView()
				continue
			}
			if event.Type == KeyPgDn {
				w.scrollOffset -= 5
				if w.scrollOffset < 0 {
					w.scrollOffset = 0
				}
				w.renderFullView()
				continue
			}
			if event.Type == KeyEnd && w.scrollOffset > 0 {
				w.scrollOffset = 0
				w.unreadNew = 0
				w.renderFullView()
				continue
			}

			// Pass key to composer line editor
			cmd, submitted := w.composer.HandleKey(event)
			// The menu follows the buffer rather than being summoned: typing a
			// trigger opens it, typing on narrows it, and typing past it closes
			// it, which is what makes it discoverable without pressing Tab.
			if !submitted {
				w.refreshCompletion()
			} else {
				w.closeCompletion()
			}
			if submitted {
				if cmd == "/quit" || cmd == "/exit" {
					return nil
				}
				w.runCommand(ctx, cmd)
				if w.commandExitRequested() {
					w.terminal.ClearScreen()
					return nil
				}
			}
			w.renderComposer()
		}
	}
}

func (w *Workspace) commandExitRequested() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.exitRequested
}

func priorityCommand(cmd string) bool {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case "/stop", "/takeover", "/take-over", "/cancel", "/focus", "/view":
		return true
	}
	return false
}

// runCommand executes a command and records its result as workspace activity.
//
// Output is stored in state and painted as part of the next frame rather than
// printed directly, so a command response cannot scroll the screen or leave
// chrome behind in scrollback.
func (w *Workspace) runCommand(ctx context.Context, cmd string) {
	if w.uiEvents != nil && priorityCommand(cmd) {
		// Safety and navigation must remain available while ordinary work runs.
		// They own neither the ordinary lane nor its cancellation token.
		w.workers.start(ctx, &w.commandWG, func(ctx context.Context) {
			commandCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			resp, err := w.cmd.Handle(commandCtx, cmd)
			w.postUI(ctx, func() { w.recordCommandResult(cmd, resp, err) })
		})
		return
	}
	if w.uiEvents != nil && !w.directTerminalCommand(cmd) {
		if w.navView != nil {
			phase := w.navView.Confirmation().Phase()
			if phase == PhasePreparing || phase == PhaseSubmitting {
				w.RecordActivity("A Control operation is still running.")
				return
			}
		}
		if !w.commandBusy.CompareAndSwap(false, true) {
			w.RecordActivity("A command is still running; Ctrl+C cancels it.")
			return
		}
		commandCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		w.commandCancelMu.Lock()
		w.commandCancel = cancel
		w.commandCancelMu.Unlock()
		w.workers.start(commandCtx, &w.commandWG, func(ctx context.Context) {
			commandCtx = ctx
			defer cancel()
			resp, err := w.cmd.Handle(commandCtx, cmd)
			_ = w.RefreshState(commandCtx)
			if !w.postUI(ctx, func() { w.recordCommandResult(cmd, resp, err); w.commandBusy.Store(false) }) {
				w.commandBusy.Store(false)
			}
		})
		return
	}
	resp, err := w.cmd.Handle(ctx, cmd)
	_ = w.RefreshState(ctx)
	w.recordCommandResult(cmd, resp, err)
	w.renderFullView()
}

func (w *Workspace) recordCommandResult(cmd, resp string, err error) {

	w.mu.Lock()
	if err != nil {
		w.state.LastOutput = fmt.Sprintf("Error: %v", err)
		if strings.TrimSpace(resp) != "" {
			w.state.LastOutput = resp + "\n" + w.state.LastOutput
		}
		w.state.LastOutputIsError = true
	} else {
		w.state.LastOutput = resp
		w.state.LastOutputIsError = false
	}
	w.state.LastOutput = activityTail(hideMarshalProtocol(w.state.LastOutput))
	w.state.LastCommand = RedactContent(cmd, w.state.KnownSecrets)
	w.mu.Unlock()
}

// SuspendTerminal temporarily leaves raw terminal mode and alternate screen,
// allowing a child interactive process to run with the actual terminal attached.
// When the returned resume function is called, raw terminal mode and alternate screen
// are restored and the workspace is redrawn.
func (w *Workspace) SuspendTerminal() func() {
	if w.terminal == nil || !w.terminal.IsTerminal() {
		return func() {}
	}
	w.terminal.DisableMouse()
	w.terminal.DisableBracketedPaste()
	w.terminal.ShowCursor()
	w.terminal.LeaveAltScreen()
	_ = w.terminal.Restore()

	return func() {
		_ = w.terminal.MakeRaw()
		w.terminal.EnterAltScreen()
		w.terminal.EnableBracketedPaste()
		w.mouseOn = false
		w.syncMouseMode()
		if w.screen != nil {
			w.screen.Reset()
		}
		w.renderFullView()
	}
}

// handleTab opens or advances the completion popup.
// syncMouseMode holds mouse reporting only while the navigation view is open.
//
// Mouse tracking makes the terminal report button presses instead of running
// its own selection, so holding it open on the composer takes drag-select and
// copy away from the operator on a surface that never reads a mouse event.
// Navigation is the only consumer, so it is the only place the mode is on.
func (w *Workspace) syncMouseMode() {
	if w.terminal == nil || !w.terminal.IsTerminal() {
		return
	}
	want := w.navView != nil && w.navView.IsOpen()
	if want == w.mouseOn {
		return
	}
	if want {
		w.terminal.EnableMouse()
	} else {
		w.terminal.DisableMouse()
	}
	w.mouseOn = want
}

// completionTrigger reports whether a word is one the live menu should open on.
//
// Only the explicit prefixes qualify. Opening on every bare word would take the
// arrow keys away from history for someone who is typing prose, not a command.
// completionTrigger reports whether what is being typed asks for the menu.
//
// A sigil asks for it outright. So does an argument of a slash command, which
// the word alone cannot tell you: after "/memory " the word is empty, and
// judging by the word closed the menu exactly where the operator had most
// reason to expect it. The line is consulted too, so completing a command's
// argument opens the same menu as completing the command.
func completionTrigger(line, word string) bool {
	if strings.HasPrefix(word, "/") || strings.HasPrefix(word, "@") || strings.HasPrefix(word, "#") {
		return true
	}
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "/")
}

// refreshCompletion recomputes the menu from what the composer currently holds.
//
// It never writes to the buffer. The operator is mid-word, and a menu that
// rewrote the line underneath them would fight their typing.
func (w *Workspace) refreshCompletion() {
	if w.completer == nil || w.composer == nil {
		return
	}
	if w.uiEvents == nil {
		qualifyProviderCompletions(context.Background(), &w.completer.ctx, w.terminal != nil && w.terminal.IsTerminal())
	} else {
		if !w.completionSkillsReady {
			w.completer.ctx.InstallableSkills = func() []string { return nil }
			w.completionSkillsReady = true
		}
		w.qualifyCompletionInBackground()
	}

	if text := w.composer.Text(); text != w.completionText {
		w.completionSelected = false
		w.completionCycled = false
		w.completionText = text
	}
	word, matches := w.completer.Suggest(w.composer.Text(), w.composer.CursorPos())
	if len(matches) == 0 || !completionTrigger(w.composer.Text(), word) {
		w.closeCompletion()
		return
	}
	// A menu offering exactly what has already been typed has nothing left to
	// complete, and leaving it open would take Enter away from submitting the
	// command the operator just finished writing.
	if len(matches) == 1 && (matches[0] == word || strings.HasPrefix(word, "/") && strings.EqualFold(matches[0], word)) {
		w.closeCompletion()
		return
	}
	// Keep the highlight on the same candidate across a keystroke where it
	// survived the narrowing, so typing another letter does not silently move
	// the selection to something else.
	selected := ""
	if w.completionOpen && w.completionIndex < len(w.completions) {
		selected = w.completions[w.completionIndex]
	}
	w.completions = matches
	w.completionIndex = 0
	for i, m := range matches {
		if m == selected {
			w.completionIndex = i
			break
		}
	}
	w.completionOpen = true
}

// Provider qualification can stat binaries and run their --version command.
// Keep one bounded probe off the input loop, retaining the current menu until
// its replacement arrives. No probe runs merely because the workspace is idle.
func (w *Workspace) qualifyCompletionInBackground() {
	if time.Since(w.completionProbeTime) < 2*time.Second || !w.completionProbeBusy.CompareAndSwap(false, true) {
		return
	}
	w.completionProbeTime = time.Now()
	completion := w.completer.ctx
	completion.Subcommands = maps.Clone(completion.Subcommands)
	completion.Descriptions = maps.Clone(completion.Descriptions)
	terminal := w.terminal != nil && w.terminal.IsTerminal()
	w.workers.start(context.Background(), &w.commandWG, func(ctx context.Context) {
		defer w.completionProbeBusy.Store(false)
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		qualifyProviderCompletions(ctx, &completion, terminal)
		skills := w.readInstallableSkills()
		publishCtx, publishCancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer publishCancel()
		w.postUI(publishCtx, func() {
			w.completer.ctx.Commands = completion.Commands
			w.mu.Lock()
			w.commandNames = append([]string(nil), completion.Commands...)
			w.mu.Unlock()
			w.completer.ctx.Subcommands = completion.Subcommands
			w.completer.ctx.Descriptions = completion.Descriptions
			w.completer.ctx.InstallableSkills = func() []string { return skills }
			w.completionProbeTime = time.Now()
			w.refreshCompletion()
		})
	})
}

// moveCompletion moves the highlight only. The buffer is written on accept.
func (w *Workspace) moveCompletion(delta int) {
	if len(w.completions) == 0 {
		return
	}
	w.completionSelected = true
	w.completionIndex = (w.completionIndex + delta + len(w.completions)) % len(w.completions)
}

func (w *Workspace) completionShouldSubmit() bool {
	return w.completingWord() == "" && !w.completionSelected
}

// completingWord returns the word the cursor sits in, which is what a
// completion would replace. Empty means nothing is being completed.
func (w *Workspace) completingWord() string {
	runes := []rune(w.composer.Text())
	cursor := w.composer.CursorPos()
	if cursor > len(runes) {
		cursor = len(runes)
	}
	start := cursor
	for start > 0 && !isWordSeparator(runes[start-1]) {
		start--
	}
	return string(runes[start:cursor])
}

// cycleCompletion steps to the next candidate and shows it in the draft.
//
// This is what Tab does with a menu open, because it is what Tab does in a
// shell: each press puts the next candidate on the line and leaves the menu up,
// so the operator reads the real thing rather than a highlight and keeps
// pressing until it is the one they meant. Tab used to take the first candidate
// and close, which answers a question the operator had not finished asking.
//
// One candidate is not a cycle: there is nothing to step through, so it is
// simply completed.
func (w *Workspace) cycleCompletion(delta int) {
	if len(w.completions) == 0 {
		return
	}
	if len(w.completions) == 1 {
		w.acceptCompletion()
		return
	}
	// The first Tab takes the candidate already highlighted rather than the one
	// after it. Moving first would skip the head of the list, which is the one
	// the menu was pointing at and the one the operator was looking at.
	if w.completionCycled {
		w.moveCompletion(delta)
	}
	w.completionSelected = true
	w.completionCycled = true
	w.writeCompletion(w.completions[w.completionIndex], false)
}

// acceptCompletion writes the highlighted candidate over the word at the cursor
// and closes the menu.
func (w *Workspace) acceptCompletion() {
	if len(w.completions) == 0 || w.completionIndex >= len(w.completions) {
		w.closeCompletion()
		return
	}
	w.writeCompletion(w.completions[w.completionIndex], true)
	w.closeCompletion()
}

// writeCompletion puts a candidate over the word at the cursor.
//
// A settled candidate is followed by a space, because the next thing typed is
// an argument rather than more of the name. One being cycled through is not:
// the trailing space would end the word and the following Tab would complete
// the next level instead of the rest of this one.
func (w *Workspace) writeCompletion(candidate string, settled bool) {
	runes := []rune(w.composer.Text())
	cursor := w.composer.CursorPos()
	if cursor > len(runes) {
		cursor = len(runes)
	}
	wordStart := cursor
	for wordStart > 0 && !isWordSeparator(runes[wordStart-1]) {
		wordStart--
	}

	if settled {
		candidate += " "
	}
	replacement := []rune(candidate)
	updated := append(append(append([]rune{}, runes[:wordStart]...), replacement...), runes[cursor:]...)
	w.composer.SetText(string(updated))
	w.composer.cursor = wordStart + len(replacement)
	w.completionText = w.composer.Text()
}

func (w *Workspace) closeCompletion() {
	w.completionOpen = false
	w.completionSelected = false
	w.completionText = ""
	w.completions = nil
	w.completionIndex = 0
	w.completionCycled = false
	w.completer.Reset()
}

// renderFullView repaints the whole workspace frame in place.
//
// Both this and renderComposer route through the same screen model, so a
// keystroke and a state change produce the same single frame. Nothing is ever
// appended to the terminal: the screen writes only rows that changed and
// addresses each one absolutely.

// dispatchNavigationKey handles the navigation view's share of the key stream.
//
// It reports whether the key was consumed, so the caller repaints and moves on.
// Extracting it means the interactive loop and the tests exercise the same
// branch: a raw-terminal loop needs a real TTY and cannot be driven directly.
func (w *Workspace) dispatchNavigationKey(ctx context.Context, event KeyEvent) bool {
	// While navigation is open it owns every key, so arrow keys and Enter
	// drive the frozen IA rather than the composer.
	if w.navView.IsOpen() {
		w.navView.HandleKey(ctx, event)
		w.mu.Lock()
		replaced := w.runtimeReplaced
		w.runtimeReplaced = false
		w.mu.Unlock()
		if replaced {
			// submitConfirmation holds NavView's lock while the destructive
			// operation runs. Rebinding here, after HandleKey returns, avoids a
			// lock inversion and guarantees every section reads the reopened
			// canonical runtime rather than its closed predecessor.
			w.openNavigation(ctx)
			w.navView.refreshInBackground(ctx)
		}
		return true
	}

	// Ctrl+N opens navigation. It is the keyboard-first entry point: from here
	// MARSHAL is fully operable without knowing a single slash command.
	if event.Type == KeyCtrlN {
		w.closeCompletion()
		if refusal := w.navigationRefusal(); refusal != "" {
			w.setOutput(refusal, false)
			return true
		}
		if w.navView == nil {
			if w.out != nil {
				reason := "the frozen interface manifest did not load"
				if w.navErr != nil {
					reason = w.navErr.Error()
				}
				fmt.Fprintf(w.out, "Navigation is unavailable: %s\n", reason)
			}
			return true
		}
		w.openNavigation(ctx)
		return true
	}
	return false
}

// navigationReleased is false while the navigation surface is incomplete and
// unverified: it has declared nodes with no capability behind them, and no
// end-to-end run has shown that it works. Until that is proven every entry
// point refuses, whatever the session's entitlement. Its own tests switch it
// on to keep exercising the code.
var navigationReleased = false

// navigationNotReleasedMessage says the surface is not offered yet, so nobody
// takes an unfinished screen for a working one.
const navigationNotReleasedMessage = "The navigation surface is not available yet: it is incomplete and has not been verified.\n" +
	"  Every MARSHAL command remains available from this composer."

// navigationRefusal says why this session may not open navigation, or ""
// when it may.
func (w *Workspace) navigationRefusal() string {
	if !w.navReleased {
		return navigationNotReleasedMessage
	}
	if !w.navigationEntitled() {
		return navigationNotEntitledMessage
	}
	return ""
}

// navigationNotEntitledMessage explains the refusal without implying the
// operator can switch it on locally: an entitlement is granted, not toggled.
const navigationNotEntitledMessage = "The navigation surface is an ULTRA feature and this session is Standard.\n" +
	"  Use /ultra to see why, or /ultra request to ask an operator for an entitlement.\n" +
	"  Every MARSHAL command remains available from this composer."

// navigationEntitled reports whether this session may open the navigation view.
//
// The gate is read live rather than captured, because the Cloud handshake
// finishes after the workspace is built: a session that becomes entitled
// mid-run must be able to open navigation without restarting.
func (w *Workspace) navigationEntitled() bool {
	gate, _ := w.ultraGate()
	return gate.Entitled()
}

// setOutput records a workspace response for the next frame, the same way a
// command result is recorded, so it cannot scroll the screen or leave chrome
// behind in scrollback.
func (w *Workspace) setOutput(text string, isError bool) {
	w.mu.Lock()
	w.state.LastOutput = text
	w.state.LastOutputIsError = isError
	w.mu.Unlock()
	w.renderFullView()
}

// interruptNavigation closes the navigation view for Ctrl+C.
//
// Ctrl+C unwinds the innermost context first, and an open navigation view is
// the innermost thing there is. It reports whether it consumed the interrupt.
func (w *Workspace) interruptNavigation() bool {
	if !w.navView.IsOpen() {
		return false
	}
	w.navView.Close()
	w.interruptArmed = false
	return true
}

// AttachRuntime wires the canonical mutation authority into the workspace.
//
// Control submits every mutation through this runtime. Without it the Control
// screens still render, and every action refuses with the reason — which is
// the truthful behaviour for a workspace that cannot reach an authority.
func (w *Workspace) AttachRuntime(runtime *app.Runtime, identity projectid.ID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.runtime = runtime
	w.projectIdentity = identity
	if runtime != nil {
		runtime.SetEgressAlertSink(w.deliverEgressAlert)
		runtime.SetPermissionSink(w.queuePermission)
	}
}

// AttachULTRARequest supplies the canonical entitlement request path.
func (w *Workspace) AttachULTRARequest(request func(ctx context.Context) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ultraRequest = request
}

// AttachControlSource wires an explicit ControlSource into the workspace,
// primarily for tests and standalone control plane scenarios.
func (w *Workspace) AttachControlSource(source *ControlSource) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.controlOverride = source
}

// controlSource builds the Control authority from the workspace's handles.
func (w *Workspace) controlSource() *ControlSource {
	w.mu.RLock()
	if w.controlOverride != nil {
		source := w.controlOverride
		w.mu.RUnlock()
		return source
	}
	runtime, identity := w.runtime, w.projectIdentity
	request, session, project := w.ultraRequest, w.sessionID, w.projectID
	store := w.store
	w.mu.RUnlock()

	if runtime == nil {
		// No authority is reachable. Returning nil here would make Control
		// render nothing; returning a source with no authority makes every
		// action refuse with the reason, which is what the user needs to see.
		return &ControlSource{SessionID: session, ProjectID: project, ApproverID: session}
	}
	controlCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	localControl, localControlErr := runtime.OpenLocalControl(controlCtx)
	return &ControlSource{
		Authority: &runtimeControlAuthority{
			runtime:         runtime,
			store:           store,
			localControl:    localControl,
			localControlErr: localControlErr,
			// The gate is read live: the Cloud handshake finishes after the
			// workspace is built, so a captured gate would report Standard
			// for the rest of the session.
			gate: func() *cloud.Gate {
				w.mu.RLock()
				defer w.mu.RUnlock()
				return w.ultra
			},
			requestULTRA: request,
			sessionID:    session,
			projectID:    identity,
			// The frozen specs make the workspace the owner of these two
			// preferences, so they are read and written here rather than
			// copied into the Control layer where nothing would see them.
			setMode: func(mode string) error {
				w.mu.Lock()
				defer w.mu.Unlock()
				w.mode = mode
				w.state.SessionMode = strings.ToUpper(mode)
				return nil
			},
			mode: func() string {
				w.mu.RLock()
				defer w.mu.RUnlock()
				return w.mode
			},
			setPreference: func(enabled bool) error {
				w.mu.Lock()
				defer w.mu.Unlock()
				// This is a preference, never an authority: the gate is
				// untouched, so with no entitlement it changes nothing.
				w.ultraExecution = enabled
				return nil
			},
			preference: func() bool {
				w.mu.RLock()
				defer w.mu.RUnlock()
				return w.ultraExecution
			},
			requestExit: func() error {
				w.mu.Lock()
				defer w.mu.Unlock()
				w.exitRequested = true
				return nil
			},
			exitRequested: func() bool {
				w.mu.RLock()
				defer w.mu.RUnlock()
				return w.exitRequested
			},
			replaceRuntime: func(reopened *app.Runtime) {
				w.mu.Lock()
				defer w.mu.Unlock()
				w.runtime = reopened
				reopened.SetEgressAlertSink(w.deliverEgressAlert)
				reopened.SetPermissionSink(w.queuePermission)
				w.store = reopened.Store()
				w.runtimeReplaced = true
			},
		},
		SessionID: session,
		ProjectID: string(identity),
		// The approver is the session acting. The backend records who decided;
		// nothing here judges whether that is self-approval.
		ApproverID: session,
	}
}

// openNavigation enters the frozen-IA navigation view.
//
// The canonical readers are attached at open time rather than at construction
// because the Cloud gate arrives after the workspace is built, and a view that
// captured a nil gate at startup would report Standard forever.
func (w *Workspace) openNavigation(ctx context.Context) {
	if w.navView == nil {
		return
	}
	if w.uiEvents != nil {
		w.navView.mu.Lock()
		w.navView.open = true
		w.navView.status, w.navView.statusSeen = "reading canonical state…", false
		w.navView.mu.Unlock()
		if !w.navigationOpening.CompareAndSwap(false, true) {
			return
		}
		w.workers.start(ctx, &w.commandWG, func(ctx context.Context) {
			defer w.navigationOpening.Store(false)
			composeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			w.attachNavigation(composeCtx)
		})
		return
	}
	w.attachNavigation(ctx)
}

func (w *Workspace) attachNavigation(ctx context.Context) {
	if w.navView == nil {
		return
	}
	w.mu.RLock()
	source := &StatusSource{
		Runtime:   w.runtimeReader(),
		Resources: defaultResourceReader{},
		// The Cloud handles are read afresh on every access rather than
		// captured here: the handshake finishes after the workspace is built,
		// and a reader holding the nil gate it saw at open time would report
		// Standard for the rest of the session.
		Cloud: &workspaceCloudReader{
			live: func() (*cloud.Gate, bool, string, string, error) {
				w.mu.RLock()
				defer w.mu.RUnlock()
				return w.ultra, w.ultraClient != nil,
					w.ultraState.InstallationID, w.ultraSession, w.ultraErr
			},
		},
	}
	w.mu.RUnlock()
	control := w.controlSource()
	var providers ProviderReader
	if control != nil {
		if authority, ok := control.Authority.(*runtimeControlAuthority); ok {
			providers = authority
		}
	}
	w.navView.AttachSource(source, providers)
	// Control is attached at open time for the same reason the Cloud reader is
	// read live: the runtime may arrive after the workspace was built.
	w.navView.AttachControl(control)
	// Work reads through the same canonical handles as Control, so the two
	// sections cannot disagree about which project and session are current.
	if authority, ok := control.Authority.(*runtimeControlAuthority); ok && authority != nil {
		w.navView.AttachWork(&WorkSource{
			Reader:    authority,
			SessionID: control.SessionID,
			ProjectID: control.ProjectID,
		})
		w.navView.AttachVerify(&VerifySource{
			Reader:    authority,
			SessionID: control.SessionID,
		})
		w.navView.AttachMemory(&MemoryFeed{
			Reader:    authority,
			SessionID: control.SessionID,
			ProjectID: control.ProjectID,
		})
		w.navView.AttachModels(&ModelsFeed{
			Reader:    authority,
			ProjectID: control.ProjectID,
		})
		w.navView.AttachSecurity(&SecurityFeed{
			Reader:    authority,
			ProjectID: control.ProjectID,
		})
		w.navView.AttachSystem(&SystemFeed{Reader: authority})
	}
	// A background refresh must reach the screen. Without this the frame sits
	// on "refreshing…" until the operator presses a key.
	w.navView.OnRepaint(w.requestRepaint)
	if w.uiEvents != nil {
		// This already runs in the owned composition worker. Finish the read
		// before its context is cancelled, preserving a close made by the user.
		w.navView.Refresh(ctx)
	} else {
		w.navView.Open(ctx)
	}
}

// OpenNavigation enters MARSHAL's frozen Community navigation surface.
//
// It is never called on startup: a session opens on the composer, which every
// user has. Navigation is an ULTRA surface, so an unentitled session is
// refused here rather than at each section, and the refusal explains itself.
// Esc at the root returns to the composer.
func (w *Workspace) OpenNavigation(ctx context.Context) bool {
	if refusal := w.navigationRefusal(); refusal != "" {
		w.setOutput(refusal, false)
		return false
	}
	w.openNavigation(ctx)
	return true
}

func (w *Workspace) renderFullView() {
	w.paint()
}

// renderComposer repaints after an input edit. It is the same frame paint; the
// screen diff means only the composer row actually reaches the terminal, so
// typing stays cheap while remaining correct.
func (w *Workspace) renderComposer() {
	w.paint()
}

func (w *Workspace) paint() {
	if w.screen == nil {
		return
	}
	cols, rows := w.terminal.Size()

	w.mu.RLock()
	state := w.liveStateLocked()
	th := w.theme
	workDir := w.workDir
	w.mu.RUnlock()

	// The navigation view owns the screen while open, as the diff viewer does.
	if w.navView.IsOpen() {
		lines := w.navView.Render(cols, rows-1)
		for len(lines) < rows {
			lines = append(lines, "")
		}
		w.screen.Render(lines, cols, rows, rows, 1)
		return
	}

	// The diff viewer and palette own the screen while open.
	if w.diffViewer.IsOpen() {
		lines := w.diffViewer.Render(cols, rows-1)
		for len(lines) < rows {
			lines = append(lines, "")
		}
		w.screen.Render(lines, cols, rows, rows, 1)
		return
	}

	var popup []string
	if w.palette.IsOpen() {
		popup = w.palette.Render(cols, rows)
	} else if w.completionOpen && len(w.completions) > 0 {
		popup = renderCompletionPopup(w.completions, w.completionIndex, th, cols, w.completer.descriptionsFor(w.composer.Text(), w.composer.CursorPos()))
	}

	frame := BuildFrame(state, th, workDir, w.composer, popup, cols, rows)
	frame.ScrollOffset = w.scrollOffset
	frame.UnreadCount = w.unreadNew
	lines, cursorRow := frame.Lines(cols, rows)
	w.screen.Render(lines, cols, rows, cursorRow, frame.CursorCol)
}

// printBatchFrame writes the workspace once for a non-interactive stream.
//
// It composes the same header, body and statusline the interactive path paints,
// so piping commands into the TUI shows the same information rather than a
// second, divergent layout.
func (w *Workspace) printBatchFrame(out io.Writer) {
	w.mu.RLock()
	state := w.liveStateLocked()
	th := w.theme
	workDir := w.workDir
	w.mu.RUnlock()

	const cols = 100
	frame := BuildFrame(state, th, workDir, w.composer, nil, cols, 40)

	for _, line := range frame.Header {
		fmt.Fprintln(out, strings.TrimRight(line, " "))
	}
	for _, line := range frame.Body {
		fmt.Fprintln(out, strings.TrimRight(line, " "))
	}
	fmt.Fprintln(out, strings.Repeat("─", cols))
	fmt.Fprintln(out, strings.TrimRight(frame.Status, " "))
}

func (w *Workspace) runLineScanner(ctx context.Context, in io.Reader, out io.Writer) error {
	defer func() {
		w.reportActiveTmuxSessions()
	}()

	// Non-interactive fallback: stdin is a pipe or file, so there is no screen
	// to address. Output is sequential by necessity, but it renders the same
	// frame content as the interactive path so both agree on what is shown.
	w.printBatchFrame(out)

	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if line == "/quit" || line == "/exit" {
			fmt.Fprintln(out, "Exiting MARSHAL terminal workspace. Any durable session data is preserved.")
			return nil
		}

		response, err := w.cmd.Handle(ctx, line)
		if response != "" {
			fmt.Fprintln(out, response)
		}
		if err != nil {
			fmt.Fprintf(out, "Error: %v\n", err)
		}

		if w.commandExitRequested() {
			return nil
		}
		_ = w.RefreshState(ctx)
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func onOff(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

// postUI transfers widget changes to the input loop. Headless callers remain synchronous.
func (w *Workspace) postUI(ctx context.Context, fn func()) bool {
	if w.uiEvents == nil {
		fn()
		return true
	}
	select {
	case w.uiEvents <- fn:
		return true
	case <-ctx.Done():
		return false
	}
}

func (w *Workspace) requestRepaint() {
	if w.repaint == nil {
		return
	}
	select {
	case w.repaint <- struct{}{}:
	default:
	}
}

func (w *Workspace) cancelCommand() {
	w.commandCancelMu.Lock()
	defer w.commandCancelMu.Unlock()
	if w.commandCancel != nil {
		w.commandCancel()
	}
}

// A direct PTY child takes exclusive stdin ownership while the workspace is
// suspended. Production control-centre sessions use tmux and stay asynchronous.
func (w *Workspace) directTerminalCommand(line string) bool {
	if tmux.IsInsideTmux() || w.terminal == nil || !w.terminal.IsTerminal() {
		return false
	}
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return false
	}
	root := strings.ToLower(parts[0])
	switch root {
	case "/doctor":
		return len(parts) > 1 && oneOf(strings.ToLower(parts[1]), "codex", "provider")
	case "/resume":
		return len(parts) > 1 && !strings.HasPrefix(parts[1], "run:")
	case "/review":
		return len(parts) != 2 || !strings.HasPrefix(parts[1], "ver-")
	case "/sandbox":
		return len(parts) > 1
	case "/marshal":
		return len(parts) == 2 && strings.EqualFold(parts[1], "chat")
	}
	if oneOf(root, "/fork", "/login", "/logout", "/mcp", "/plugin", "/plugins", "/features", "/search") {
		return true
	}
	if !oneOf(root, "/codex", "/claude", "/opencode", "/agy", "/antigravity") {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	sub := strings.ToLower(parts[1])
	if strings.HasPrefix(sub, "\"") || strings.HasPrefix(sub, "'") {
		return true
	}
	return !oneOf(sub, "status", "info", "health", "help", "sessions", "runs", "history", "models", "model", "select", "skills", "skill", "diff", "apply", "exec", "dispatch") || oneOf(root, "/opencode", "/agy", "/antigravity") && sub == "models"
}

func (w *Workspace) readInstallableSkills() []string {
	source := w.controlSource()
	if source == nil {
		return nil
	}
	reader, ok := source.Authority.(interface {
		LocalCodexSkills() ([]codex.SkillInfo, error)
	})
	if !ok {
		return nil
	}
	skills, err := reader.LocalCodexSkills()
	if err != nil {
		return nil
	}
	var names []string
	for _, skill := range skills {
		if skill.Installable {
			names = append(names, skill.Name)
		}
	}
	return names
}
