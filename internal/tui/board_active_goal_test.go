package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

type activeGoalFakeArch struct{}

func (activeGoalFakeArch) ArchitectureStatus(context.Context) (app.ArchitectureStatus, error) {
	return app.ArchitectureStatus{}, nil
}
func (activeGoalFakeArch) Open() {}

func activeGoalBoardWorkspace(t *testing.T, st *store.Store, session string) *Workspace {
	t.Helper()
	w := NewWorkspace(st, "demo", session)
	w.workDir = t.TempDir()
	w.theme = NewTheme(ThemeNoColor, true, false)
	w.screen = NewScreen(NewTerminal(strings.NewReader(""), &strings.Builder{}))
	w.homeBoard = true
	w.archSource = activeGoalFakeArch{}
	w.endOpening()
	return w
}

func activeGoalFrameText(w *Workspace, cols, rows int) string {
	lines, _, _ := w.frameLines(cols, rows)
	return StripANSI(strings.Join(lines, "\n"))
}

// TestBoardRefreshPicksUpActiveGoalFromStore verifies that when a goal contract
// is saved into goal_active after the Board opened, the architecture follower
// refreshes it off the paint path so the home and header switch from NO RUN YET
// to PLANNING and show the active goal; cancelling or delivering the goal
// returns the Board to NO RUN YET.
func TestBoardRefreshPicksUpActiveGoalFromStore(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "board-goal.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	w := activeGoalBoardWorkspace(t, st, "session-active-goal")
	defer w.Close()

	// READY with no goal, PLANNING with the goal named on the Web Board's
	// Plan pane while one is active.
	before := activeGoalFrameText(w, 120, 40)
	if !strings.Contains(lines0(before), " READY ") || !strings.Contains(before, "the Marshal is waiting for a goal") {
		t.Fatalf("before goal exists:\n%s", before)
	}

	goal := model.GoalContract{
		ID:                 "GOAL-live-1",
		SessionID:          w.sessionID,
		ProjectID:          "demo",
		Revision:           1,
		OriginalRequest:    "Build a terminal RSS reader",
		DesiredOutcome:     "Build a terminal RSS reader with keyboard navigation",
		Confirmation:       model.ConfirmationApproved,
		Risk:               model.R1,
		AuthoritySource:    "owner",
		UnderstandingState: model.GoalReady,
		CreatedAt:          time.Now().UTC().Add(-25 * time.Minute),
	}
	if err := st.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("SaveGoalContract: %v", err)
	}

	w.refreshArch(ctx)
	during := activeGoalFrameText(w, 120, 40)
	head := lines0(during)
	if !strings.Contains(head, " PLANNING ") || strings.Contains(head, " READY ") {
		t.Fatalf("top bar after goal saved to store: %q", head)
	}
	if b := w.boardV2(); !strings.Contains(b.Plan.Title, "Build a terminal RSS reader with keyboard navigation") {
		t.Fatalf("the Plan pane does not name the goal: %q", b.Plan.Title)
	}
	if !strings.Contains(during, "Now:       the Marshal is ") {
		t.Fatalf("the Now line does not say the Marshal works on the goal:\n%s", during)
	}

	// A goal already delivered by a closed run returns to NO RUN YET.
	done := goal.OriginalRequest
	w.closedGoal.Store(&done)
	delivered := activeGoalFrameText(w, 120, 40)
	if !strings.Contains(lines0(delivered), " READY ") {
		t.Fatalf("delivered goal did not return to idle home:\n%s", delivered)
	}
	w.closedGoal.Store(nil)

	// Cancelling the goal in the store returns to NO RUN YET on the next refresh.
	if _, err := st.CancelGoalContract(ctx, w.sessionID, goal.ID, goal.Revision, "owner cancelled"); err != nil {
		t.Fatalf("CancelGoalContract: %v", err)
	}
	w.refreshArch(ctx)
	cancelled := activeGoalFrameText(w, 120, 40)
	if !strings.Contains(lines0(cancelled), " READY ") || strings.Contains(cancelled, " PLANNING ") {
		t.Fatalf("cancelled goal did not return to NO RUN YET:\n%s", cancelled)
	}
}

// TestActiveGoalWithOwnerGateShowsNeedsYou verifies that when a goal is active
// AND an owner gate (e.g. architecture review) is waiting, NEEDS YOU takes
// precedence over PLANNING in the header while the body still shows the goal
// being worked on rather than the first-use call to action.
func TestActiveGoalWithOwnerGateShowsNeedsYou(t *testing.T) {
	w := activeGoalBoardWorkspace(t, nil, "session-gate")
	defer w.Close()
	w.mu.Lock()
	w.state.Goal = model.GoalContract{
		ID:             "GOAL-gate",
		Revision:       1,
		DesiredOutcome: "Build hello-uz CLI",
		Confirmation:   model.ConfirmationApproved,
		CreatedAt:      time.Now().Add(-5 * time.Minute),
	}
	w.mu.Unlock()
	w.gateContractSource = func(context.Context) (*gateContract, error) {
		return &gateContract{ID: "hello-uz", Revision: 1, Digest: "sha256:9089e7832c5c", Purpose: "Build hello-uz", Submitted: time.Now()}, nil
	}
	w.refreshGates(t.Context())

	text := activeGoalFrameText(w, 120, 40)
	if !strings.Contains(text, "From you:  architecture waits for you") || strings.Contains(text, "From you:  nothing") {
		t.Fatalf("From you with active goal and pending gate:\n%s", text)
	}
}

func TestGoalElapsedFormatting(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-5 * time.Second, "0s"},
		{0, "0s"},
		{45 * time.Second, "45s"},
		{time.Minute, "1m"},
		{12 * time.Minute, "12m"},
		{80 * time.Minute, "1h20m"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
	}
	for _, tc := range cases {
		if got := goalElapsed(tc.d); got != tc.want {
			t.Errorf("goalElapsed(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// TestActiveGoalFallbacksAndHints verifies fallback summary behavior (OriginalRequest
// when DesiredOutcome is blank, generic fallback when both are blank without ever
// showing goal ID) and that footer hints switch from say.build to marshal.say.
func TestActiveGoalFallbacksAndHints(t *testing.T) {
	w := activeGoalBoardWorkspace(t, nil, "session-fallback")
	defer w.Close()

	// OriginalRequest fallback when DesiredOutcome is empty.
	w.mu.Lock()
	w.state.Goal = model.GoalContract{
		ID:              "GOAL-secret-id",
		Revision:        1,
		OriginalRequest: "Add dark mode support\nwith user toggle",
		Confirmation:    model.ConfirmationPending,
	}
	w.mu.Unlock()
	text := activeGoalFrameText(w, 120, 40)
	if title := w.boardV2().Plan.Title; !strings.Contains(title, "Add dark mode support") || strings.Contains(title, "with user toggle") {
		t.Fatalf("did not fall back to first line of OriginalRequest: %q", title)
	}
	if strings.Contains(text, "GOAL-secret-id") || strings.Contains(lines0(text), " READY ") {
		t.Fatalf("leaked goal ID or kept NO RUN YET:\n%s", text)
	}

	// Minimal goal with neither DesiredOutcome nor OriginalRequest uses fallback line.
	w.mu.Lock()
	w.state.Goal = model.GoalContract{
		ID:           "GOAL-minimal",
		Revision:     1,
		Confirmation: model.ConfirmationApproved,
	}
	w.mu.Unlock()
	text = activeGoalFrameText(w, 120, 40)
	if !strings.Contains(lines0(text), " PLANNING ") || !strings.Contains(w.boardV2().Plan.Title, "your goal") || !strings.Contains(text, "working on your goal") {
		t.Fatalf("minimal goal did not render fallback cleanly:\n%s", text)
	}
	if strings.Contains(text, "GOAL-minimal") {
		t.Fatalf("minimal goal leaked ID or kept CTA:\n%s", text)
	}

}
