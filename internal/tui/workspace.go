package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
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
	mu        sync.RWMutex
	store     *store.Store
	coord     *collaboration.Coordinator
	router    *harness.ULTRARouter
	projectID string
	sessionID string
	mode      string
	state     UIState
	cmd       *CommandHandler
}

// NewWorkspace instantiates a terminal workspace connected to the canonical store.
func NewWorkspace(st *store.Store, projectID, sessionID string) *Workspace {
	if sessionID == "" {
		sessionID = fmt.Sprintf("tui-session-%d", time.Now().UnixNano())
	}
	ws := &Workspace{
		store:     st,
		coord:     collaboration.NewCoordinator(st, nil),
		router:    harness.NewULTRARouter(nil),
		projectID: projectID,
		sessionID: sessionID,
		mode:      "ultra",
		state: UIState{
			ProjectID:          projectID,
			SessionID:          sessionID,
			SessionMode:        "ULTRA",
			UnderstandingState: model.GoalReady,
		},
	}
	ws.cmd = NewCommandHandler(ws)
	return ws
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

// RefreshState pulls current ground truth from canonical SQLite tables.
func (w *Workspace) RefreshState(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.store == nil {
		return nil
	}

	// 1. Recover active GoalContract for this session
	goal, err := w.store.GetActiveGoalContract(ctx, w.sessionID)
	if err == nil {
		w.state.Goal = goal
		w.state.UnderstandingState = goal.UnderstandingState

		// 2. Recover Claims for this goal
		claims, err := w.store.ListClaimsByGoal(ctx, goal.ID, goal.Revision)
		if err == nil {
			w.state.Claims = claims
		}

		// 3. Recover Budget for this goal
		budget, err := w.store.GetBudgetTracker(ctx, w.sessionID, goal.ID, goal.Revision)
		if err == nil && budget != nil {
			w.state.BudgetConsumed = *budget
		}

		// 4. Recover Termination status if any
		term, err := w.store.GetGoalTermination(ctx, w.sessionID, goal.ID, goal.Revision)
		if err == nil && term != nil {
			w.state.TerminationState = term.State
		}
	}

	// 5. Recover Collaborative Session if existing
	sess, err := w.store.GetTeamSession(ctx, w.sessionID)
	if err == nil && sess != nil {
		w.state.Participants = sess.Participants
		w.state.ActiveTurn = sess.ActiveTurn
	} else if len(w.state.Participants) == 0 {
		// Initialize default canonical team participants
		w.state.Participants = []model.Participant{
			{AgentID: "claude", Role: model.RoleArchitect, Harness: "claude-code", Model: "claude-3-7-sonnet", IsActive: true},
			{AgentID: "codex", Role: model.RoleDeveloper, Harness: "codex", Model: "gpt-4o", IsActive: true},
			{AgentID: "opencode", Role: model.RoleQA, Harness: "opencode", Model: "deepseek-coder", IsActive: true},
			{AgentID: "antigravity", Role: model.RoleAppSec, Harness: "antigravity", Model: "gemini-2.5-pro", IsActive: true},
		}
	}

	// 6. Recover recent messages
	msgs, err := w.store.ListAgentMessages(ctx, w.sessionID, 30)
	if err == nil {
		w.state.RecentMessages = msgs
	}

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
// It is fully keyboard-driven, respects silence by default, and never corrupts task state on EOF/exit.
func (w *Workspace) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	_ = w.RefreshState(ctx)

	// Initial render
	w.mu.RLock()
	screen := RenderScreen(w.state, 90)
	w.mu.RUnlock()
	fmt.Fprint(out, screen)
	fmt.Fprint(out, "\nmarshal> ")

	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Fprint(out, "marshal> ")
			continue
		}

		if line == "/quit" || line == "/exit" {
			fmt.Fprintln(out, "Exiting MARSHAL terminal workspace. Session remains durable.")
			return nil
		}

		// Handle command
		response, err := w.cmd.Handle(ctx, line)
		if err != nil {
			fmt.Fprintf(out, "Error: %v\n", err)
		} else if response != "" {
			fmt.Fprintln(out, response)
		}

		// Refresh state and prompt again
		_ = w.RefreshState(ctx)
		fmt.Fprint(out, "\nmarshal> ")
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}

	return nil
}
