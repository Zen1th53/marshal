package tui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The live Mac demo: the runtime imported the Marshal's plan, Activity said
// "the Marshal approved it and the tasks start now", and nothing ran for
// twenty minutes. The Marshal had published the draft and waited for the
// runtime to accept it before sending its approval, and the runtime never
// told it the draft was in: only a refusal woke it. The Board said PLANNING
// with "nothing" from the owner.
//
// An imported plan with no approval behind it now wakes the Marshal and asks
// for the approval; Activity says only what happened; and a run that makes
// no progress past planStallBound becomes something the owner is asked to
// look at, with the reason. This includes a recovered hand-in with no
// reviewer driving the run, even when the remaining tasks are queued.

// planStallBound is how long an imported plan may wait for its approval, or
// an approved plan for its first worker, or a hand-in for its reviewer,
// before the Board asks the owner.
var planStallBound = 5 * time.Minute

// stallWatch remembers since when the Board has seen the current run stuck
// in the same way.
type stallWatch struct {
	mu    sync.Mutex
	key   string
	since time.Time
}

// planStall is a run that has not moved: why, and since when. reason is a
// format with one %s for the time it has not moved.
type planStall struct {
	since  time.Time
	reason string
}

func (s *planStall) overdue(now time.Time) bool {
	return s != nil && now.Sub(s.since) > planStallBound
}

// stallCondition names how the run on the Board is stuck, "" when it is
// not: an imported plan nobody approved, queued work with no worker, or
// hand-ins and queued work with no reviewer or live lane. Nothing is paused
// and nothing else waits for the owner (a limits card or a gate is the ask).
func stallCondition(in flowIn) (key, reason string) {
	if !in.hasRun || in.fixingPlan() || in.panel == nil || in.panel.RunID == "" {
		return "", ""
	}
	b := in.v.board
	if b.Run.UserPaused || in.run.Pause != nil || in.v.ownerWaits() {
		return "", ""
	}
	if _, ok := in.v.stopNotice(); ok {
		return "", ""
	}
	c := countTasks(b.Tasks)
	switch runState(in.run, b) {
	case marshal.Drafting:
		if c.total == 0 {
			return "", ""
		}
		return "approve:" + in.panel.RunID, "plan in, not approved by the Marshal for %s"
	case marshal.Approved, marshal.Dispatching, marshal.Reviewing:
		open := c.total - c.merged - c.cancelled
		if open == 0 || in.v.liveLanes() > 0 {
			return "", ""
		}
		if c.review > 0 && c.queued+c.review == open && !b.Run.Driving {
			return "review:" + in.panel.RunID, "hand-in waiting for review for %s"
		}
		if c.queued != open {
			return "", ""
		}
		return "dispatch:" + in.panel.RunID, "no worker on the queued tasks for %s"
	}
	return "", ""
}

// stallSince returns the stall the Board sees now, timing it from the first
// frame that saw it; any change of condition or run starts it again.
func (w *Workspace) stallSince(key, reason string, now time.Time) *planStall {
	s := &w.stall
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		s.key, s.since = "", time.Time{}
		return nil
	}
	if s.key != key {
		s.key, s.since = key, now
	}
	return &planStall{since: s.since, reason: reason}
}

// stallAsk is the owner's line for an overdue stall; the Board keeps an
// ask within 72 characters, so the reason is a short sentence with the
// time in it.
func stallAsk(s *planStall, now time.Time) string {
	return fmt.Sprintf(s.reason, goalElapsed(now.Sub(s.since))) + ": open the Marshal chat"
}

// planImportedNotice is what the Marshal chat is told when its plan is in
// and no approval of it is pending.
func planImportedNotice(runID string, tasks int) string {
	return marshalNotePrefix + fmt.Sprintf("The runtime imported your plan draft: run %s with %s. It is waiting for your approval and no task starts until it applies. Send the approve proposal {\"action\":\"approve\"} now, through the same channel as your earlier proposals.", runID, count(tasks, "task", "tasks"))
}

// afterPlanImported finishes an import: it applies an approval the Marshal
// already sent, or else wakes the Marshal for it. It returns the Activity
// line, which says the tasks start only when an approval was applied.
func (w *Workspace) afterPlanImported(runID string, tasks int) string {
	if w.queueMarshalPlanApproval() {
		return planReadyActivity(tasks, true)
	}
	notice := planImportedNotice(runID, tasks)
	if view, err := openMarshalInbox(w.workDir); err == nil {
		if view.write(fmt.Sprintf("## runtime · %s · plan imported\n\n%s\n", time.Now().UTC().Format(time.RFC3339), notice)) == nil {
			_ = view.trim()
		}
	}
	w.tellMarshal(notice)
	return planReadyActivity(tasks, false)
}

// tellMarshal wakes the Marshal through the runtime notice path, off the
// caller's locks and on the workspace's own context (a short caller context
// ended the delivery before the chat got it, see tellMarshalRunClosed).
// Without an open chat the inbox note waits for the Marshal's next turn.
// draftTell replaces the delivery in tests.
func (w *Workspace) tellMarshal(text string) {
	if w.draftTell != nil {
		w.draftTell(text)
		return
	}
	if w.sayPane() == "" && !w.nativeChatActive() {
		return
	}
	ctx := w.workers.context()
	w.startBackground(ctx, func(context.Context) {
		w.noticeToMarshal(ctx, text)
	})
}
