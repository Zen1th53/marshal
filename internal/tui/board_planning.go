package tui

import (
	"context"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// planningCard is the first-use card's one line between the owner's
// architecture Accept and the run it plans.
const planningCard = "The Marshal is planning the tasks…"

// planningNow reports whether the Board shows the planning state: no run
// exists yet, the goal is not cancelled, and the architecture is accepted.
// The acceptance is the review's stored state (the follower reads it at
// start and every 10 s while the Board shows, archSnapshotNow), so a restart
// mid-planning still says the Marshal is planning; the in-memory flag the
// browser Accept sets covers the seconds before that read. A stored
// acceptance counts only with a goal on record. Once a run is on screen the
// flag ends for good.
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
	if done := w.closedGoal.Load(); done != nil && *done == goal.OriginalRequest {
		return false
	}
	return true
}

// readClosedGoal records the goal the latest stored run delivered, when that
// run is closed. It reads the store, so it runs on the architecture
// follower, never on the paint path.
func (w *Workspace) readClosedGoal(ctx context.Context) {
	if w.store == nil {
		return
	}
	_, record, err := w.store.LatestMarshalRun(ctx, w.projectID)
	if err != nil || record.Value.State != marshal.Closed {
		w.closedGoal.Store(nil)
		return
	}
	goal := record.Value.GoalBinding
	w.closedGoal.Store(&goal)
}
