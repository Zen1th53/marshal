package tui

import (
	"context"
	"sync"
	"time"
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
	mu     sync.Mutex
	since  time.Time
	stage  string
	reason string
}

// draftRejectMark is a copy of draftRejectState for one Board frame.
type draftRejectMark struct {
	since         time.Time
	stage, reason string
}

func (w *Workspace) markDraftRejected(stage, reason string, now time.Time) {
	d := &w.draftReject
	d.mu.Lock()
	d.since, d.stage, d.reason = now, stage, reason
	d.mu.Unlock()
	w.requestRepaint()
}

// clearDraftRejected ends the refusal once a draft was imported.
func (w *Workspace) clearDraftRejected() {
	d := &w.draftReject
	d.mu.Lock()
	had := !d.since.IsZero()
	d.since, d.stage, d.reason = time.Time{}, "", ""
	d.mu.Unlock()
	if had {
		w.requestRepaint()
	}
}

func (w *Workspace) draftRejectedNow() *draftRejectMark {
	d := &w.draftReject
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.since.IsZero() {
		return nil
	}
	return &draftRejectMark{since: d.since, stage: d.stage, reason: d.reason}
}

// overdue reports whether the Marshal has had longer than draftFixBound.
func (m *draftRejectMark) overdue(now time.Time) bool {
	return m != nil && now.Sub(m.since) > draftFixBound
}

// draftRejectNotice is what the Marshal chat is told. The reason is
// runtime-written (app.DraftRejectionReason for a validation refusal, the
// redacted runtime error otherwise); no draft text is repeated.
func draftRejectNotice(reason string) string {
	text := marshalNotePrefix + "The runtime refused your plan draft"
	if reason != "" {
		text += ". Reason: " + reason
	} else {
		text += "; the reason is in " + marshalInboxFile
	}
	return text + ". Fix what it names, write the corrected " + marshalDraftRelativePath + " (and plan pack if needed), then publish " + marshalDraftRelativePath + ".ready again: the watcher imports it. The owner sees that you are fixing the plan."
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
