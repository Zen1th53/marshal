package tui

import (
	"context"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/board/vm"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

// noWorkerFor reports how long a run the header would call running has had
// no live worker, once that is longer than model.WorkerBeatGrace. A run is
// running only while a worker runs it: a governed worker that heartbeats or
// a worker window. While the Marshal itself reviews, merges or checks, or a
// decision waits in the inbox, no worker is expected and nothing is said.
func (v boardVisual) noWorkerFor() (time.Duration, bool) {
	b := v.board
	if b.Run.WorkerSeen.IsZero() || v.now.IsZero() || b.Run.UserPaused || len(b.Inbox) > 0 {
		return 0, false
	}
	switch b.Run.State {
	case marshal.Approved, marshal.Dispatching:
	default:
		return 0, false
	}
	if v.liveLanes() > 0 {
		return 0, false
	}
	for _, t := range b.Tasks {
		switch t.State {
		case model.TaskReview, model.TaskAccepted, "verifying":
			return 0, false
		}
	}
	quiet := v.now.Sub(b.Run.WorkerSeen)
	if quiet <= model.WorkerBeatGrace {
		return 0, false
	}
	return quiet, true
}

// quietFor is a quiet time as the header says it: "4m", "1h05m".
func quietFor(d time.Duration) string {
	return strings.TrimSuffix(hostAgo(d), " ago")
}

// quietNote is the run's note as the footer says it. A note that the run
// is running, left from when it started, is replaced while no worker runs
// it: what happened, and how to move the run on.
func (v boardVisual) quietNote(note string) string {
	if !strings.Contains(note, "running") && note != "resuming" {
		return note
	}
	quiet, ok := v.noWorkerFor()
	if !ok {
		if why := v.unverifiedWorker(); why != "" {
			return why
		}
		return note
	}
	return "not running: no worker for " + quietFor(quiet) + " · /marshal stop, then /marshal resume"
}

// unverifiedWorker is the first lane whose worker Marshal cannot verify or
// that has waited long on its model, as the footer says it; "" for none.
func (v boardVisual) unverifiedWorker() string {
	for _, l := range v.board.Lanes {
		var row vm.Lane
		laneLiveness(&row, l, v.now)
		if row.Note != "" && (row.Process == app.ProcessUnverifiable || row.ModelWait >= app.ModelWaitNoticeAfter) {
			return "worker on " + l.TaskID + ": " + row.Note
		}
	}
	return ""
}

// laneLiveness gives a Lanes row what Marshal can verify of its heartbeat
// worker at now: whether its process is live and its turn, how long it has
// waited on its model, and a line saying why it cannot be verified.
func laneLiveness(row *vm.Lane, l model.Lane, now time.Time) {
	if !l.Heartbeat {
		return
	}
	live := l.Liveness
	row.Process, row.Turn, row.Note = live.Process, live.Turn, live.Note
	if l.State == model.LaneLost && row.Note == "" {
		row.Process, row.Turn = app.ProcessUnverifiable, app.TurnUnknown
		row.Note = "no heartbeat for " + quietFor(now.Sub(l.Beat)) + "; the worker may still be running, Marshal cannot verify it"
	}
	if !live.ModelWaitSince.IsZero() && now.After(live.ModelWaitSince) {
		row.ModelWait = now.Sub(live.ModelWaitSince)
		if row.ModelWait >= app.ModelWaitNoticeAfter {
			row.Note = "waiting on its model for " + quietFor(row.ModelWait)
		}
	}
}

// signalModelWaits announces each worker wait on its model that has gone
// on long enough, and hands the notice to the Marshal's chat at once; a
// chat that is closed gets it when it opens again.
func (w *Workspace) signalModelWaits(ctx context.Context) {
	rt := w.beatRuntime.Load()
	if rt == nil {
		return
	}
	for _, runID := range rt.SignalModelWaits(context.WithoutCancel(ctx)) {
		w.deliverApprovalNotices(context.WithoutCancel(ctx), runID)
	}
}
