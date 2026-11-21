package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// TestBoardRefreshShowsPlanningMilestone verifies the architecture follower
// reads the planning milestone from the goal's records off the paint path, the
// home card shows it, and a cancelled goal clears it.
func TestBoardRefreshShowsPlanningMilestone(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "board-milestone.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	w := activeGoalBoardWorkspace(t, st, "session-milestone")
	defer w.Close()

	goal := model.GoalContract{
		ID:                 "GOAL-milestone-1",
		SessionID:          w.sessionID,
		ProjectID:          "demo",
		Revision:           1,
		OriginalRequest:    "Build a terminal RSS reader",
		DesiredOutcome:     "Build a terminal RSS reader",
		Confirmation:       model.ConfirmationApproved,
		SuccessCriteria:    []string{"feeds refresh on r"},
		Risk:               model.R1,
		AuthoritySource:    "owner",
		UnderstandingState: model.GoalReady,
		CreatedAt:          time.Now().UTC().Add(-10 * time.Minute),
	}
	if err := st.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("SaveGoalContract: %v", err)
	}
	w.refreshArch(ctx)
	m := w.milestone.Load()
	if m == nil || m.Step != 2 || m.Now != "reading the code" || m.Since.IsZero() {
		t.Fatalf("milestone after confirmed requirements = %+v", m)
	}
	since := m.Since
	if text := activeGoalFrameText(w, 120, 40); !strings.Contains(text, "the Marshal is reading the code · ") {
		t.Fatalf("home misses milestone:\n%s", text)
	}

	// The same step on the next read keeps its first-seen time.
	w.refreshArch(ctx)
	if got := w.milestone.Load(); got == nil || !got.Since.Equal(since) {
		t.Fatalf("unchanged milestone restarted its clock: %+v", got)
	}

	if _, err := st.CancelGoalContract(ctx, w.sessionID, goal.ID, goal.Revision, "owner cancelled"); err != nil {
		t.Fatalf("CancelGoalContract: %v", err)
	}
	w.refreshArch(ctx)
	if got := w.milestone.Load(); got != nil {
		t.Fatalf("cancelled goal kept its milestone: %+v", got)
	}
}
