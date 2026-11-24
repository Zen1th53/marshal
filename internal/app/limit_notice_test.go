package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestRecordLimitNoticePersistsRedactsAndDeduplicates(t *testing.T) {
	rt := runtimeForPlan(t)
	ctx := t.Context()

	limit := model.Limit{
		Kind:        "design_record",
		Subject:     "design artifact secret=" + runNoticeSecret,
		ActualBytes: 1<<20 + 1,
		CapBytes:    1 << 20,
		Action:      "Record a smaller artifact instead of " + runNoticeSecret,
	}
	if err := rt.RecordLimitNotice(ctx, "RUN-limit", "design_record", limit); err != nil {
		t.Fatal(err)
	}
	// Repeating the same refusal is idempotent.
	if err := rt.RecordLimitNotice(ctx, "RUN-limit", "design_record", limit); err != nil {
		t.Fatal(err)
	}
	// Non-limit errors record nothing.
	if err := rt.RecordLimitError(ctx, "RUN-limit", "design_record", errors.New("plain error")); err != nil {
		t.Fatal(err)
	}

	pending, err := rt.Store().PendingNotices(ctx, rt.ProjectIdentity(), "RUN-limit", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending notices = %d, want 1: %+v", len(pending), pending)
	}
	entry := pending[0]
	if entry.Category != LimitNoticeCategory || entry.Transition != "refused" || entry.Status != "design_record" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if strings.Contains(entry.Summary, runNoticeSecret) || strings.Contains(entry.Command, runNoticeSecret) || strings.Contains(entry.PayloadJSON, runNoticeSecret) {
		t.Fatalf("secret leaked into notice outbox: %+v", entry)
	}
	var payload LimitNotice
	if err := json.Unmarshal([]byte(entry.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Source != "design_record" || payload.Limit.ActualBytes != 1<<20+1 || payload.Limit.CapBytes != 1<<20 || !payload.Limit.Limited {
		t.Fatalf("payload: %+v", payload)
	}

	// Failed delivery keeps the notice pending; retry delivers once and acks it.
	if _, err := rt.DeliverPendingNotices(ctx, "marshal-chat", "RUN-limit", func(store.NoticeOutboxEntry, string) error {
		return errors.New("chat unavailable")
	}); err == nil {
		t.Fatal("expected delivery failure")
	}
	var delivered []store.NoticeOutboxEntry
	acked, err := rt.DeliverPendingNotices(ctx, "marshal-chat", "RUN-limit", func(e store.NoticeOutboxEntry, _ string) error {
		delivered = append(delivered, e)
		return nil
	})
	if err != nil || acked != 1 || len(delivered) != 1 || delivered[0].EventID != entry.EventID {
		t.Fatalf("deliver: acked=%d delivered=%+v err=%v", acked, delivered, err)
	}
	if left, err := rt.Store().PendingNotices(ctx, rt.ProjectIdentity(), "RUN-limit", 10); err != nil || len(left) != 0 {
		t.Fatalf("left pending: %+v %v", left, err)
	}

	events, err := rt.Store().ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, ev := range events {
		if ev.Type == "marshal.limit_notice" && ev.ID == entry.EventID {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("marshal.limit_notice audit events = %d, want 1", found)
	}
}
