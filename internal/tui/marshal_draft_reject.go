package tui

import (
	"context"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
)

// A refused Marshal draft used to be reported only as a fixed line in
// Activity and the inbox file: the Marshal sat idle without the reason and
// the Board said "STARTING" and "nothing" from the owner indefinitely. The
// refusal now carries a runtime-written reason, wakes the Marshal through
// the runtime notice path, and stays on the Board until a draft is imported;
// past draftFixBound it becomes something the owner is asked to look at.

// draftFixBound is how long the Marshal has to publish a corrected draft
// before the Board asks the owner to step in.
var draftFixBound = 10 * time.Minute

// draftRejectState is the last refused draft not yet followed by an
// imported one.
type draftRejectState struct {
	mu   sync.Mutex
	mark draftRejectMark
}

// draftRejectMark is one refusal: when, on which watched run, at which
// stage and with which runtime-written reason.
type draftRejectMark struct {
	since                time.Time
	runID, stage, reason string
}

func (w *Workspace) markDraftRejected(runID, stage, reason string, now time.Time) {
	d := &w.draftReject
	d.mu.Lock()
	d.mark = draftRejectMark{since: now, runID: runID, stage: stage, reason: reason}
	d.mu.Unlock()
	w.requestRepaint()
}

// clearDraftRejected ends the refusal once a draft was imported or the
// owner discarded the handoff.
func (w *Workspace) clearDraftRejected() {
	d := &w.draftReject
	d.mu.Lock()
	had := !d.mark.since.IsZero()
	d.mark = draftRejectMark{}
	d.mu.Unlock()
	if had {
		w.requestRepaint()
	}
}

func (w *Workspace) draftRejectedNow() *draftRejectMark {
	d := &w.draftReject
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.mark.since.IsZero() {
		return nil
	}
	mark := d.mark
	return &mark
}

// overdue reports whether the Marshal has had longer than draftFixBound.
func (m *draftRejectMark) overdue(now time.Time) bool {
	return m != nil && now.Sub(m.since) > draftFixBound
}

// draftRejectNotice is what the Marshal chat is told. reason is only ever
// app.DraftRejectionReason's field path and rule; for the other stages it is
// empty and the notice points at the inbox note, which carries the redacted
// runtime reason. No draft text is repeated.
//
// The watcher took both handoff parts before judging them (the draft is
// kept as plan-draft.json.consumed, the pack moved under the run), so the
// corrected handoff is both again, then the marker.
func draftRejectNotice(reason string) string {
	text := marshalNotePrefix + "The runtime refused your plan draft"
	if reason != "" {
		text += ". Reason: " + reason + "."
	} else {
		text += "; read the reason in " + marshalInboxFile + "."
	}
	return text + " Fix what it names. The runtime took the draft and its plan pack, so write the whole plan pack again in " + app.MarshalPackRelativePath + "/ and the corrected " + marshalDraftRelativePath + ", read both back, then publish " + marshalDraftRelativePath + ".ready: the watcher imports it. The owner sees that you are fixing the plan."
}

// tellMarshalDraftRejected wakes the Marshal with the refusal through the
// runtime notice path, off the caller's locks and on the workspace's own
// context (a short caller context ended the delivery before the chat got
// it, see tellMarshalRunClosed). Without an open chat the inbox note waits
// for the Marshal's next turn.
func (w *Workspace) tellMarshalDraftRejected(text string) {
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
