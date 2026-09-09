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

	"github.com/Zen1th53/marshal/internal/collaboration"
	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/model"
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
	terminal   *Terminal
	out        io.Writer

	// screen owns in-place frame painting. Without it every redraw appended to
	// scrollback, so each keystroke left another prompt banner behind.
	screen *Screen

	// Completion popup state. Tab is completion only: it opens or cycles this
	// list and never submits, so it can never execute a partially typed command.
	completionOpen  bool
	completions     []string
	completionIndex int
	completionStem  string

	// interruptArmed records that a Ctrl+C arrived with nothing left to
	// interrupt. A second consecutive press then exits; any other key disarms
	// it, so a stray interrupt never closes the workspace on its own.
	interruptArmed bool

	// Scroll and activity unread tracking
	scrollOffset int
	unreadNew    int
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

	compCtx := CompletionContext{
		Commands: []string{
			"/status", "/goal", "/mode", "/claims", "/inspect", "/approve", "/reject",
			"/route", "/agents", "/evidence", "/why", "/msg", "/handoff", "/checkpoint",
			"/rollback", "/budget", "/pause", "/resume", "/cancel", "/doctor", "/tasks",
			"/policy", "/sandbox", "/memory", "/provider", "/harness", "/model", "/effort",
			"/ultra", "/backup", "/fingerprint", "/runtime", "/store", "/export", "/blind",
			"/reinjection", "/alignment", "/optimization", "/diff", "/help", "/quit",
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

	completer := NewCompleter(compCtx)
	composer := NewComposer(th)
	composer.SetPrompt(ComposerPromptInfo{
		Project: projectID,
		Mode:    "MANUAL",
		State:   "NO_GOAL",
	})

	paletteActions := GlobalRegistry.ToPaletteActions()
	palette := NewCommandPalette(th, paletteActions)
	diffViewer := NewDiffViewer(th, cwd)

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
	_ = w.RefreshState(ctx)

	// Check if running in a real interactive terminal
	if w.terminal != nil && w.terminal.IsTerminal() && in == os.Stdin && out == os.Stdout {
		return w.runRawTerminal(ctx)
	}

	// Fallback for piped or non-terminal environments
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
	w.screen = NewScreen(w.terminal)
	defer func() {
		w.terminal.DisableBracketedPaste()
		w.terminal.ShowCursor()
		w.terminal.LeaveAltScreen()
		w.terminal.Restore()
	}()

	w.renderFullView()

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
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.terminal.ResizeEvents():
			// A resize invalidates the diff baseline: the previous frame was
			// laid out for the old geometry.
			w.screen.Reset()
			w.renderFullView()
		case kr := <-keys:
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

			// 'd' opens the diff viewer only when the composer is empty, so it
			// never swallows a character the operator is typing.
			if event.Type == KeyRune && event.Rune == 'd' && w.composer.Text() == "" {
				_ = w.diffViewer.Open()
				w.renderFullView()
				continue
			}

			// While the completion popup is open it owns the arrow keys, Enter and
			// Esc, so selection is never confused with history navigation or submit.
			if w.completionOpen {
				switch event.Type {
				case KeyEsc:
					w.closeCompletion()
					w.renderComposer()
					continue
				case KeyUp:
					w.cycleCompletion(-1)
					w.renderComposer()
					continue
				case KeyDown:
					w.cycleCompletion(1)
					w.renderComposer()
					continue
				case KeyEnter:
					// Enter accepts the highlighted candidate and closes the popup.
					// It does not also submit: accepting a completion and running a
					// command are two deliberate keystrokes.
					w.acceptCompletion()
					w.renderComposer()
					continue
				}
			}

			// Tab is completion, never submission. It opens the popup on the first
			// press and cycles thereafter; it never inserts a newline, never
			// reprints the prompt and never executes the buffer.
			if event.Type == KeyTab || event.Type == KeyShiftTab {
				w.handleTab(event.Type == KeyShiftTab)
				w.renderComposer()
				continue
			}

			// Any other key invalidates a stale completion list.
			if w.completionOpen {
				w.closeCompletion()
			}
			w.completer.Reset()

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

// handleTab opens or advances the completion popup.
func (w *Workspace) handleTab(reverse bool) {
	text := w.composer.Text()
	cursor := w.composer.CursorPos()

	newText, newCursor, ok := w.completer.Complete(text, cursor, reverse)
	if !ok {
		w.closeCompletion()
		return
	}

	w.composer.SetText(newText)
	w.composer.cursor = newCursor

	matches := w.completer.ActiveMatches()
	if len(matches) <= 1 {
		// A single unambiguous candidate is simply completed; there is nothing
		// to choose between, so no popup is shown.
		w.closeCompletion()
		return
	}

	w.completions = matches
	w.completionOpen = true
	prefix := newText[:newCursor]
	for i, m := range matches {
		if strings.HasSuffix(prefix, m) {
			w.completionIndex = i
			break
		}
	}
}

// cycleCompletion moves the highlight and applies that candidate to the buffer,
// so the composer always shows exactly what accepting would produce.
func (w *Workspace) cycleCompletion(delta int) {
	if len(w.completions) == 0 {
		return
	}
	w.completionIndex = (w.completionIndex + delta + len(w.completions)) % len(w.completions)

	text := w.composer.Text()
	cursor := w.composer.CursorPos()
	runes := []rune(text)
	if cursor > len(runes) {
		cursor = len(runes)
	}
	wordStart := cursor
	for wordStart > 0 && !isWordSeparator(runes[wordStart-1]) {
		wordStart--
	}

	candidate := w.completions[w.completionIndex]
	newRunes := append(append(append([]rune{}, runes[:wordStart]...), []rune(candidate)...), runes[cursor:]...)
	w.composer.SetText(string(newRunes))
	w.composer.cursor = wordStart + len([]rune(candidate))
}

// acceptCompletion keeps the highlighted candidate and dismisses the popup.
func (w *Workspace) acceptCompletion() {
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
