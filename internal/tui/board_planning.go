package tui

import (
	"context"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// planningCard is the first-use card's one line between the owner's
// architecture Accept and the run it plans.
const planningCard = "The Marshal is planning the tasks…"

// planningNow reports whether the Board shows the post-accept planning state:
// no run exists yet, the goal is not cancelled, and the architecture is
// accepted. The acceptance is the review's stored state (the follower reads it
// at start and every 10 s while the Board shows, archSnapshotNow), so a restart
// mid-planning still says the Marshal is planning; the in-memory flag the
// browser Accept sets covers the seconds before that read. A stored acceptance
// counts only with a goal on record. Once a run is on screen the flag ends for
// good.
func (w *Workspace) planningNow(empty bool, goal model.GoalContract) bool {
	if !empty {
		w.planning.Store(false)
		return false
	}
	if goal.Confirmation == model.ConfirmationCancelled {
		return false
	}
	if w.planning.Load() {
		return true
	}
	snap := w.archSnapshotNow()
	if goal.ID == "" || !snap.Loaded || !snap.Status.Accepted {
		return false
	}
	// An accepted architecture whose goal a closed run already delivered
	// is history, not planning.
	if w.goalDelivered(goal) {
		return false
	}
	return true
}

// activeGoalNow returns the active, undelivered goal when no run exists yet.
// Cancelled goals and goals a closed run already delivered are history, so the
// home card returns to asking what to build.
func (w *Workspace) activeGoalNow(empty bool, goal model.GoalContract) (model.GoalContract, bool) {
	if !empty || goal.ID == "" || goal.Confirmation == model.ConfirmationCancelled {
		return model.GoalContract{}, false
	}
	if w.goalDelivered(goal) {
		return model.GoalContract{}, false
	}
	return goal, true
}

// goalDelivered reports whether the latest closed run already delivered goal.
func (w *Workspace) goalDelivered(goal model.GoalContract) bool {
	done := w.closedGoal.Load()
	if done == nil || *done == "" {
		return false
	}
	return *done == goal.OriginalRequest || (goal.OriginalRequest == "" && *done == goal.DesiredOutcome)
}

// goalSummaryText is the active goal's plain-English one-line summary for the
// home card: the desired outcome when set, else the original request, redacted
// and stripped of control characters. It never falls back to the goal ID.
func goalSummaryText(g model.GoalContract) string {
	raw := firstNonEmpty(firstLine(g.DesiredOutcome), firstLine(g.OriginalRequest))
	if raw == "" {
		return ""
	}
	return strings.TrimSpace(cleanBoard(RedactContent(raw, nil)))
}

// goalStartedAt is when the active goal was given (CreatedAt, falling back to
// UpdatedAt when CreatedAt is zero).
func goalStartedAt(g model.GoalContract) time.Time {
	if !g.CreatedAt.IsZero() {
		return g.CreatedAt
	}
	return g.UpdatedAt
}

// goalStore returns the store to read goal and closed-run state from, preferring
// w.store and falling back to the attached runtime's store.
func (w *Workspace) goalStore() *store.Store {
	w.mu.RLock()
	st, rt := w.store, w.runtime
	w.mu.RUnlock()
	if st == nil && rt != nil {
		st = rt.Store()
	}
	return st
}

// refreshActiveGoal reads the session's active goal (goal_active) and latest
// closed run's delivered goal off the paint path, repaints when either changes,
// so a goal written outside the TUI command loop (e.g. MCP or CLI while the
// Marshal plans) updates the home card and header promptly.
func (w *Workspace) refreshActiveGoal(ctx context.Context) {
	st := w.goalStore()
	w.mu.RLock()
	session := w.sessionID
	prev := w.state.Goal
	w.mu.RUnlock()
	if st == nil || session == "" {
		return
	}
	goal, err := st.GetActiveGoalContract(ctx, session)
	if ctx.Err() != nil {
		return
	}
	changed := false
	if err == nil {
		if goalChanged(prev, goal) {
			w.mu.Lock()
			w.state.Goal = goal
			w.mu.Unlock()
			changed = true
		}
		if w.readClosedGoal(ctx) {
			changed = true
		}
	}
	if changed && w.boardVisible() {
		w.requestRepaint()
	}
}

func goalChanged(a, b model.GoalContract) bool {
	return a.ID != b.ID ||
		a.Revision != b.Revision ||
		a.Confirmation != b.Confirmation ||
		a.DesiredOutcome != b.DesiredOutcome ||
		a.OriginalRequest != b.OriginalRequest ||
		!a.CreatedAt.Equal(b.CreatedAt) ||
		!a.UpdatedAt.Equal(b.UpdatedAt)
}

// readClosedGoal records the goal the latest stored run delivered, when that
// run is closed, and reports whether the cached value changed. It reads the
// store, so it runs on the architecture follower, never on the paint path.
func (w *Workspace) readClosedGoal(ctx context.Context) bool {
	st := w.goalStore()
	if st == nil {
		return false
	}
	_, record, err := st.LatestMarshalRun(ctx, w.projectID)
	prev := w.closedGoal.Load()
	if err != nil || record.Value.State != marshal.Closed {
		w.closedGoal.Store(nil)
		return prev != nil
	}
	goal := record.Value.GoalBinding
	w.closedGoal.Store(&goal)
	return prev == nil || *prev != goal
}
