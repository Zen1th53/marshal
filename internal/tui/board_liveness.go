package tui

import (
	"strings"
	"time"

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
	quiet, ok := v.noWorkerFor()
	if !ok || (!strings.Contains(note, "running") && note != "resuming") {
		return note
	}
	return "not running: no worker for " + quietFor(quiet) + " · /marshal stop, then /marshal resume"
}
