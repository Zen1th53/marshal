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

	// interruptArmed records that a Ctrl+C arrived with nothing left to
	// interrupt. A second consecutive press then exits; any other key disarms
	// it, so a stray interrupt never closes the workspace on its own.
	interruptArmed bool
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

	compCtx := CompletionContext{
		Commands: []string{
			"/status", "/goal", "/mode", "/claims", "/inspect", "/approve", "/reject",
			"/route", "/agents", "/evidence", "/why", "/msg", "/handoff", "/checkpoint",
			"/rollback", "/budget", "/pause", "/resume", "/cancel", "/doctor", "/tasks",
			"/policy", "/sandbox", "/memory", "/provider", "/harness", "/model", "/effort",
			"/ultra", "/backup", "/fingerprint", "/runtime", "/store", "/export", "/blind",
			"/reinjection", "/alignment", "/diff", "/help", "/quit",
		},
		Agents:      []string{"claude", "codex", "opencode", "antigravity"},
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
		Mode:    "ULTRA",
		State:   "READY",
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
		mode:       "ultra",
		theme:      th,
		composer:   composer,
		completer:  completer,
		palette:    palette,
		diffViewer: diffViewer,
		terminal:   NewTerminal(os.Stdin, os.Stdout),
		state: UIState{
			ProjectID:          projectID,
			SessionID:          sessionID,
			SessionMode:        "ULTRA",
			UnderstandingState: model.GoalReady,
			GitStatus:          ProbeGitStatus(cwd),
			Participants:       DiscoverTeamParticipants(nil),
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
		w.state.RecentMessages = msgs
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
	defer w.terminal.Restore()

	w.renderFullView()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.terminal.ResizeEvents():
			w.renderFullView()
		default:
			event, err := w.terminal.ReadKey()
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
				w.palette.Toggle()
				w.renderFullView()
				continue
			}

			if w.palette.IsOpen() {
				action, executed := w.palette.HandleKey(event)
				if executed && action != nil {
					w.renderFullView()
					resp, cmdErr := w.cmd.Handle(ctx, action.Command)
					_ = w.RefreshState(ctx)
					w.renderFullView()
					if cmdErr != nil {
						fmt.Printf("\r\nError: %v\r\n", cmdErr)
					} else if resp != "" {
						fmt.Printf("\r\n%s\r\n", resp)
					}
					w.renderComposer()
					continue
				}
				w.renderFullView()
				continue
			}

			// Handle Diff Viewer (d / Esc)
			if w.diffViewer.IsOpen() {
				handled := w.diffViewer.HandleKey(event)
				if handled {
					w.renderFullView()
					continue
				}
			}

			// Handle 'd' key to open diff viewer when composer buffer is empty
			if event.Type == KeyRune && event.Rune == 'd' && w.composer.Text() == "" {
				_ = w.diffViewer.Open()
				w.renderFullView()
				continue
			}

			// Handle Tab / Shift+Tab autocomplete
			if event.Type == KeyTab || event.Type == KeyShiftTab {
				reverse := (event.Type == KeyShiftTab)
				newText, newCursor, ok := w.completer.Complete(w.composer.Text(), w.composer.CursorPos(), reverse)
				if ok {
					w.composer.SetText(newText)
					w.composer.cursor = newCursor
					w.renderComposer()
				}
				continue
			} else {
				w.completer.Reset()
			}

			// Pass key to composer line editor
			cmd, submitted := w.composer.HandleKey(event)
			if submitted {
				if cmd == "/quit" || cmd == "/exit" {
					w.terminal.ClearScreen()
					fmt.Println("Exiting MARSHAL terminal workspace. Session remains durable.")
					return nil
				}

				w.terminal.ClearScreen()
				resp, cmdErr := w.cmd.Handle(ctx, cmd)
				_ = w.RefreshState(ctx)
				w.renderFullView()
				if cmdErr != nil {
					fmt.Printf("\r\nError: %v\r\n", cmdErr)
				} else if resp != "" {
					fmt.Printf("\r\n%s\r\n", resp)
				}
				w.renderComposer()
				continue
			}

			w.renderComposer()
		}
	}
}

func (w *Workspace) renderFullView() {
	width, height := w.terminal.Size()
	w.terminal.ClearScreen()

	w.mu.RLock()
	state := w.state
	th := w.theme
	w.mu.RUnlock()

	// If diff viewer is open, render diff viewer instead of main screen
	if w.diffViewer.IsOpen() {
		lines := w.diffViewer.Render(width, height-4)
		for _, l := range lines {
			fmt.Print(l + "\r\n")
		}
		return
	}

	screen := RenderStyledScreen(state, th, width)
	lines := strings.Split(screen, "\n")
	for _, l := range lines {
		fmt.Print(l + "\r\n")
	}

	// Overlay Command Palette if open
	if w.palette.IsOpen() {
		palLines := w.palette.Render(width, height)
		fmt.Print("\r\n")
		for _, pl := range palLines {
			fmt.Print(pl + "\r\n")
		}
	}

	w.renderComposer()
}

func (w *Workspace) renderComposer() {
	w.terminal.CursorMoveToCol(1)
	w.terminal.ClearLine()
	fmt.Print(w.composer.Render())
}

func (w *Workspace) runLineScanner(ctx context.Context, in io.Reader, out io.Writer) error {
	w.mu.RLock()
	screen := RenderStyledScreen(w.state, w.theme, 90)
	w.mu.RUnlock()
	fmt.Fprint(out, screen)
	fmt.Fprint(out, "\n"+w.composer.Render())

	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Fprint(out, "\n"+w.composer.Render())
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
		fmt.Fprint(out, "\n"+w.composer.Render())
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}
