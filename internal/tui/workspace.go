package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

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
	// ultraExecution is the user's ULTRA Execution preference. It is not an
	// authority: with no entitlement it changes nothing.
	ultraExecution bool
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
	completionOpen bool
	// mouseOn tracks whether mouse reporting is currently held, so the mode is
	// only written to the terminal when it actually changes.
	mouseOn         bool
	completions     []string
	completionIndex int
	completionStem  string

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

	// Scroll and activity unread tracking
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
			"/rollback", "/budget", "/pause", "/resume", "/cancel", "/doctor", "/tasks",
			"/policy", "/sandbox", "/memory", "/provider", "/harness", "/model", "/models",
			"/effort", "/ultra", "/backup", "/fingerprint", "/runtime", "/store", "/export",
			"/blind", "/reinjection", "/alignment", "/optimization", "/diff", "/review",
			"/codex", "/claude", "/opencode", "/agy", "/antigravity", "/mcp", "/plugin", "/plugins", "/apply", "/sessions", "/fork",
			"/search", "/features", "/skill", "/skills", "/login", "/logout", "/help", "/quit",
		},
		Agents:      agentIDs,
		Subcommands: make(map[string][]string),
	}
	compCtx.Subcommands["/goal"] = []string{"create", "edit", "diff", "version", "criteria", "constraints", "add-constraint", "rm-constraint", "donotdo", "progress"}
	compCtx.Subcommands["/task"] = []string{"create", "inspect", "assign", "pause", "resume", "cancel", "retry", "ownership"}
	compCtx.Subcommands["/policy"] = []string{"network", "sandbox", "capability", "scope", "write", "audit"}
	compCtx.Subcommands["/checkpoint"] = []string{"list", "create", "inspect", "diff"}
	compCtx.Subcommands["/memory"] = []string{"search", "provenance"}
	compCtx.Subcommands["/harness"] = []string{"probe", "status", "select"}
	compCtx.Subcommands["/provider"] = []string{"status", "config"}
	compCtx.Subcommands["/alignment"] = []string{"scope", "violations", "blast", "deletions", "resolve", "status"}
	compCtx.Subcommands["/codex"] = []string{"doctor", "models", "model", "review", "sessions", "mcp", "plugin", "apply", "diff", "resume", "fork", "agents", "features", "sandbox", "approval", "search", "login", "logout", "skill", "run", "exec", "cli"}
	compCtx.Subcommands["/mcp"] = []string{"list", "add", "rm"}
	compCtx.Subcommands["/claude"] = []string{"new", "continue", "resume", "fork", "cli", "status", "models", "model", "doctor", "sessions", "exec", "run", "mcp", "plugin", "auth", "agents", "login", "logout"}
	compCtx.Subcommands["/opencode"] = []string{"new", "continue", "resume", "fork", "cli", "status", "models", "providers", "auth", "mcp", "agent", "session", "stats", "run", "debug"}
	compCtx.Subcommands["/agy"] = []string{"new", "continue", "resume", "cli", "status", "models", "agents", "mcp", "plugin", "changelog"}
	compCtx.Subcommands["/antigravity"] = compCtx.Subcommands["/agy"]
	compCtx.Subcommands["/plugin"] = []string{"list", "add", "rm"}
	compCtx.Subcommands["/plugins"] = []string{"list", "add", "rm"}
	compCtx.Subcommands["/search"] = []string{"on", "off"}
	compCtx.Subcommands["/sandbox"] = []string{"read-only", "workspace-write"}
	compCtx.Subcommands["/resume"] = []string{"--last"}
	compCtx.Subcommands["/fork"] = []string{"--last"}

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
		store:      st,
		coord:      collaboration.NewCoordinator(st, nil),
		router:     harness.NewULTRARouter(nil),
		projectID:  projectID,
		sessionID:  sessionID,
		workDir:    cwd,
		mode:       "manual",
		theme:      th,
		composer:   composer,
		completer:  completer,
		palette:    palette,
		diffViewer: diffViewer,
		navView:    navView,
		navErr:     navErr,
		terminal:   NewTerminal(os.Stdin, os.Stdout),
		state: UIState{
			ProjectID:          projectID,
			SessionID:          sessionID,
			SessionMode:        "MANUAL",
			UnderstandingState: model.GoalNeedsInput,
			GitStatus:          ProbeGitStatus(cwd),
			Participants:       participants,
		},
	}
	ws.cmd = NewCommandHandler(ws)
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
	w.mu.Lock()
	defer w.mu.Unlock()

	// 1. Live Git status
	w.state.GitStatus = ProbeGitStatus(w.workDir)

	// 2. Reconcile participants with real live probes (honest state)
	w.state.Participants = DiscoverTeamParticipants(w.state.Participants)

	if w.store == nil {
		return nil
	}

	// 3. Recover active GoalContract for this session
	goal, err := w.store.GetActiveGoalContract(ctx, w.sessionID)
	if err == nil {
		w.state.Goal = goal
		w.state.UnderstandingState = goal.UnderstandingState

		// 4. Recover Claims for this goal
		claims, err := w.store.ListClaimsByGoal(ctx, goal.ID, goal.Revision)
		if err == nil {
			w.state.Claims = claims
		}

		// 5. Recover Budget for this goal
		budget, err := w.store.GetBudgetTracker(ctx, w.sessionID, goal.ID, goal.Revision)
		if err == nil && budget != nil {
			w.state.BudgetConsumed = *budget
		}

		// 6. Recover Termination status if any
		term, err := w.store.GetGoalTermination(ctx, w.sessionID, goal.ID, goal.Revision)
		if err == nil && term != nil {
			w.state.TerminationState = term.State
		}
	}

	// 7. Recover Collaborative Session if existing
	sess, err := w.store.GetTeamSession(ctx, w.sessionID)
	if err == nil && sess != nil {
		w.state.Participants = DiscoverTeamParticipants(sess.Participants)
		w.state.ActiveTurn = sess.ActiveTurn
	}

	// 8. Recover recent messages
	msgs, err := w.store.ListAgentMessages(ctx, w.sessionID, 30)
	if err == nil {
		if w.scrollOffset > 0 && len(msgs) > len(w.state.RecentMessages) {
			w.unreadNew += len(msgs) - len(w.state.RecentMessages)
		}
		w.state.RecentMessages = msgs
	}

	// 8b. Recover pending approvals
	pending, err := w.store.ListPendingApprovals(ctx, w.state.ProjectID)
	if err == nil {
		w.state.PendingApprovals = pending
	}

	// 9. Update autocomplete context with live objects
	var claimIDs []string
	for _, c := range w.state.Claims {
		claimIDs = append(claimIDs, c.ID)
	}
	var agentIDs []string
	for _, p := range w.state.Participants {
		agentIDs = append(agentIDs, p.AgentID)
	}

	tasks, _ := w.store.ListTasks(ctx)
	var taskIDs []string
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
	}

	var cpIDs []string
	if w.store != nil {
		checkpoints, _ := w.store.ListHandoffCheckpoints(ctx, "task-interactive")
		for _, cp := range checkpoints {
			cpIDs = append(cpIDs, cp.ID)
		}
	}

	compCtx := w.completer.ctx
	compCtx.Claims = claimIDs
	compCtx.Agents = agentIDs
	compCtx.Tasks = taskIDs
	compCtx.Checkpoints = cpIDs
	w.completer.UpdateContext(compCtx)

	// 10. Update prompt info
	w.composer.SetPrompt(ComposerPromptInfo{
		Project: w.projectID,
		Mode:    strings.ToUpper(w.mode),
		State:   string(w.state.UnderstandingState),
		Agent:   "",
	})

	return nil
}

// GetUIState returns a snapshot copy of current UI state.
func (w *Workspace) GetUIState() UIState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state
}

// ExecuteCommand executes an interactive slash command.
func (w *Workspace) ExecuteCommand(ctx context.Context, line string) (string, error) {
	res, err := w.cmd.Handle(ctx, line)
	if err != nil {
		return "", err
	}
	_ = w.RefreshState(ctx)
	return res, nil
}

// Run starts the interactive terminal loop.
// Supports full raw mode line editing, Tab autocomplete, Ctrl+P palette, diff viewer,
// or clean fallback to buffered scanner if non-terminal.
func (w *Workspace) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	w.out = out

	// A navigation refresh runs off the input loop and reads the canonical
	// runtime, which creates its state directories on first use. Returning
	// while one is in flight would let it recreate .marshal after the caller
	// had closed the runtime and removed the project, so the session waits for
	// its own reads on every exit path, including an error or a panic.
	defer w.navView.Wait()

	_ = w.RefreshState(ctx)

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
	if w.out == nil {
		w.out = os.Stdout
	}
	if err := w.terminal.MakeRaw(); err != nil {
		return w.runLineScanner(ctx, os.Stdin, os.Stdout)
	}

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
	}()

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
	defer close(readerDone)

	go func() {
		for {
			ev, err := w.terminal.ReadKey()
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
				continue
			}

			// Ctrl+C is an interrupt, not a quit. It unwinds the innermost
			// context first: an open overlay, then pending composer input. Only
			// when there is nothing left to interrupt does a second consecutive
			// press exit, so a reflexive Ctrl+C never discards a session the
			// operator is still working in.
			if event.Type == KeyCtrlC {
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
					fmt.Fprintln(w.out, "Press Ctrl+C again to exit, or /quit. The session stays durable either way.")
					continue
				}
				w.terminal.ClearScreen()
				fmt.Fprintln(w.out, "Exiting MARSHAL terminal workspace. Session remains durable.")
				return nil
			}

			// Any other key cancels a pending exit confirmation.
			w.interruptArmed = false

			// Navigation owns every key while it is open, and Ctrl+N opens it.
			if event.Type == KeyF7 {
				w.runCommand(ctx, "/codex new")
				continue
			}
			if event.Type == KeyF8 {
				w.runCommand(ctx, "/claude new")
				continue
			}
			if event.Type == KeyF9 {
				w.runCommand(ctx, "/opencode new")
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
			if event.Type == KeyF12 {
				w.runCommand(ctx, "/agy new")
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
					fmt.Fprintln(w.out, "Exiting MARSHAL terminal workspace. Session remains durable.")
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
				if w.diffViewer.IsOpen() {
					w.diffViewer.Close()
				} else {
					_ = w.diffViewer.Open()
				}
				w.renderFullView()
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
				// Navigation is an ULTRA surface, so Esc on an empty composer
				// must not drop a Standard session into it.
				if !w.navigationEntitled() {
					w.setOutput(navigationNotEntitledMessage, false)
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
					w.moveCompletion(-1)
					w.renderComposer()
					continue
				case KeyTab:
					// Tab completes and nothing else. It writes the highlighted
					// candidate into the draft and stops there, so the operator
					// can still add arguments before running anything.
					w.acceptCompletion()
					w.renderComposer()
					continue
				case KeyEnter:
					// Enter picks the highlighted command and runs it. Choosing
					// from the menu is the decision; making it cost a second
					// keystroke only means pressing Enter twice.
					w.acceptCompletion()
					cmd, submitted := w.composer.HandleKey(KeyEvent{Type: KeyEnter})
					if submitted {
						if cmd == "/quit" || cmd == "/exit" {
							return nil
						}
						w.runCommand(ctx, cmd)
					}
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
			}
			w.renderComposer()
		}
	}
}

// runCommand executes a command and records its result as workspace activity.
//
// Output is stored in state and painted as part of the next frame rather than
// printed directly, so a command response cannot scroll the screen or leave
// chrome behind in scrollback.
func (w *Workspace) runCommand(ctx context.Context, cmd string) {
	resp, err := w.cmd.Handle(ctx, cmd)
	_ = w.RefreshState(ctx)

	w.mu.Lock()
	if err != nil {
		w.state.LastOutput = fmt.Sprintf("Error: %v", err)
		w.state.LastOutputIsError = true
	} else {
		w.state.LastOutput = resp
		w.state.LastOutputIsError = false
	}
	w.state.LastCommand = RedactContent(cmd, w.state.KnownSecrets)
	w.mu.Unlock()

	w.renderFullView()
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
func completionTrigger(word string) bool {
	return strings.HasPrefix(word, "/") || strings.HasPrefix(word, "@") || strings.HasPrefix(word, "#")
}

// refreshCompletion recomputes the menu from what the composer currently holds.
//
// It never writes to the buffer. The operator is mid-word, and a menu that
// rewrote the line underneath them would fight their typing.
func (w *Workspace) refreshCompletion() {
	if w.completer == nil || w.composer == nil {
		return
	}
	word, matches := w.completer.Suggest(w.composer.Text(), w.composer.CursorPos())
	if len(matches) == 0 || !completionTrigger(word) {
		w.closeCompletion()
		return
	}
	// A menu offering exactly what has already been typed has nothing left to
	// complete, and leaving it open would take Enter away from submitting the
	// command the operator just finished writing.
	if len(matches) == 1 && matches[0] == word {
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

// moveCompletion moves the highlight only. The buffer is written on accept.
func (w *Workspace) moveCompletion(delta int) {
	if len(w.completions) == 0 {
		return
	}
	w.completionIndex = (w.completionIndex + delta + len(w.completions)) % len(w.completions)
}

// acceptCompletion writes the highlighted candidate over the word at the cursor.
func (w *Workspace) acceptCompletion() {
	if len(w.completions) == 0 || w.completionIndex >= len(w.completions) {
		w.closeCompletion()
		return
	}
	candidate := w.completions[w.completionIndex]

	runes := []rune(w.composer.Text())
	cursor := w.composer.CursorPos()
	if cursor > len(runes) {
		cursor = len(runes)
	}
	wordStart := cursor
	for wordStart > 0 && !isWordSeparator(runes[wordStart-1]) {
		wordStart--
	}

	// A completed command is followed by a space: the next thing typed is an
	// argument, not more of the command name.
	replacement := []rune(candidate + " ")
	updated := append(append(append([]rune{}, runes[:wordStart]...), replacement...), runes[cursor:]...)
	w.composer.SetText(string(updated))
	w.composer.cursor = wordStart + len(replacement)
	w.closeCompletion()
}

func (w *Workspace) closeCompletion() {
	w.completionOpen = false
	w.completions = nil
	w.completionIndex = 0
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
			w.navView.Refresh(ctx)
		}
		return true
	}

	// Ctrl+N opens navigation. It is the keyboard-first entry point: from here
	// MARSHAL is fully operable without knowing a single slash command.
	if event.Type == KeyCtrlN {
		w.closeCompletion()
		if !w.navigationEntitled() {
			w.setOutput(navigationNotEntitledMessage, false)
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
	return &ControlSource{
		Authority: &runtimeControlAuthority{
			runtime: runtime,
			store:   store,
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
				w.store = reopened.Store()
				w.runtimeReplaced = true
			},
		},
		SessionID: session,
		ProjectID: project,
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
	w.navView.OnRepaint(func() { w.renderFullView() })
	w.navView.Open(ctx)
}

// OpenNavigation enters MARSHAL's frozen Community navigation surface.
//
// It is never called on startup: a session opens on the composer, which every
// user has. Navigation is an ULTRA surface, so an unentitled session is
// refused here rather than at each section, and the refusal explains itself.
// Esc at the root returns to the composer.
func (w *Workspace) OpenNavigation(ctx context.Context) bool {
	if !w.navigationEntitled() {
		w.setOutput(navigationNotEntitledMessage, false)
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
	state := w.state
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
		popup = renderCompletionPopup(w.completions, w.completionIndex, th, cols)
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
	state := w.state
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
			fmt.Fprintln(out, "Exiting MARSHAL terminal workspace. Session remains durable.")
			return nil
		}

		response, err := w.cmd.Handle(ctx, line)
		if err != nil {
			fmt.Fprintf(out, "Error: %v\n", err)
		} else if response != "" {
			fmt.Fprintln(out, response)
		}

		_ = w.RefreshState(ctx)
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}
