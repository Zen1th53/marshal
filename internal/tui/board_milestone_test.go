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
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// TestHomeCardShowsPlanningMilestone verifies the home card names the
// Marshal's planning step (done steps, the step now with how long, the steps
// to come) under the working-on line, keeps each row in width, and on a
// narrow card keeps what happens now.
func TestHomeCardShowsPlanningMilestone(t *testing.T) {
	now := time.Date(2026, 11, 8, 14, 2, 31, 0, time.UTC)
	visual := func(th *Theme) boardVisual {
		v := activeGoalVisual(th, now)
		v.goalGiven = true
		v.goalText = "Build a voice assistant with local transcription"
		v.goalSince = now.Add(-20 * time.Minute)
		v.milestone = &planningMark{
			PlanningMilestone: app.PlanningMilestone{Step: 2, Now: "drafting and reviewing the design"},
			Since:             now.Add(-7 * time.Minute),
		}
		return v
	}
	paint := func(v boardVisual, cols, rows int) string {
		l := layout.ComputeFor(cols, rows, layout.Home, v.layoutCounts())
		frame := paintFirstUse(l, v.theme, v)
		for y, line := range frame {
			if got := VisibleLen(line); got != cols {
				t.Fatalf("row %d: width %d, want %d: %q", y, got, cols, line)
			}
		}
		return StripANSI(strings.Join(frame, "\n"))
	}

	text := paint(visual(NewTheme(ThemeNoColor, true, false)), 120, 40)
	want := "goal ✓ → requirements ✓ → drafting and reviewing the design · 7m → architecture → tasks"
	if !strings.Contains(text, want) {
		t.Fatalf("home misses milestone %q:\n%s", want, text)
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.Contains(line, "The Marshal is working on:") {
			if i+1 >= len(lines) || !strings.Contains(lines[i+1], want) {
				t.Fatalf("milestone is not right under the working-on line:\n%s", text)
			}
		}
	}

	narrow := paint(visual(NewTheme(ThemeNoColor, true, false)), 80, 24)
	if !strings.Contains(narrow, "goal ✓ → requirements ✓ → drafting and reviewing the design · 7m") || strings.Contains(narrow, "→ architecture") {
		t.Fatalf("narrow card must keep the step now and drop the steps to come:\n%s", narrow)
	}
	if got := StripANSI(visual(NewTheme(ThemeNoColor, true, false)).milestoneLine(52)); got != "step 3 of 5: drafting and reviewing the design · 7m" {
		t.Fatalf("narrowest milestone = %q", got)
	}

	ascii := paint(visual(NewTheme(ThemeNoColor, false, false)), 120, 40)
	if strings.ContainsFunc(ascii, func(r rune) bool { return r > 127 }) {
		t.Fatalf("ASCII theme emitted non-ASCII cell:\n%s", ascii)
	}
	if !strings.Contains(ascii, "goal v -> requirements v -> drafting and reviewing the design . 7m -> architecture -> tasks") {
		t.Fatalf("ASCII home misses milestone:\n%s", ascii)
	}

	// No milestone known yet: the card is as before.
	v := visual(NewTheme(ThemeNoColor, true, false))
	v.milestone = nil
	if plain := paint(v, 120, 40); strings.Contains(plain, "requirements ✓") {
		t.Fatalf("milestone shown without one:\n%s", plain)
	}
}

// TestBoardRefreshShowsPlanningMilestone verifies the architecture follower
// reads the planning milestone from the goal's records off the paint path, the
// home card shows it, and a cancelled goal clears it.
func TestBoardRefreshShowsPlanningMilestone(t *testing.T) {
	// This test is about the classic Board itself.
	t.Setenv("MARSHAL_BOARD", "classic")
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
	if text := activeGoalFrameText(w, 120, 40); !strings.Contains(text, "goal ✓ → requirements ✓ → reading the code · ") {
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
