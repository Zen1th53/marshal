package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// RunNoticeLimited is the state of a notice for a live submission that was
// refused at a byte cap before it was admitted.
const RunNoticeLimited = "limited"

// RecordLimitNotice durably records the notice of a live submission refused
// at a byte cap: one outbox entry and its audit event, which the Boards'
// Activity and the Marshal chat both read. A repeated event ID records
// nothing and reports false. It admits no submission and changes nothing
// else in the project.
func (r *Runtime) RecordLimitNotice(ctx context.Context, n RunNotice) (bool, error) {
	if r == nil || r.store == nil {
		return false, model.ErrUnavailable
	}
	if n.At.IsZero() {
		n.At = time.Now().UTC()
	}
	n.State = RunNoticeLimited
	n.Summary = boundedRedacted(n.Summary, 500)
	n.Next = boundedRedacted(n.Next, 500)
	payload, err := json.Marshal(n)
	if err != nil {
		return false, err
	}
	data := map[string]any{"run_id": n.RunID, "state": n.State, "code": n.Code, "summary": n.Summary, "next": n.Next}
	if n.Limit != nil {
		data["limit"] = *n.Limit
	}
	entry := store.NoticeOutboxEntry{EventID: n.EventID, ProjectID: r.ProjectIdentity(), RunID: n.RunID, Category: RunNoticeCategory,
		Transition: n.State, PhaseOrder: 1, Status: n.Code, Command: n.Next, Summary: n.Summary, PayloadJSON: string(payload), CreatedAt: n.At}
	return r.store.RecordNoticeWithEvent(context.WithoutCancel(ctx), entry, model.Event{ID: n.EventID, Type: "marshal.run_notice", ProjectID: r.ProjectID(), Timestamp: n.At, Data: data})
}
