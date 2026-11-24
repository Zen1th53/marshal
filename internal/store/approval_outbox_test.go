package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestNoticeRetentionAndNewest(t *testing.T) {
	s, ctx := newApprovalStore(t)
	now := time.Now().UTC()
	old := now.Add(-40 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("notice-%d", i)
		entry := NoticeOutboxEntry{EventID: id, ProjectID: "proj-approval", RunID: "run", ApprovalID: id, Category: "approval", Transition: "expired", CreatedAt: old}
		event := model.Event{ID: id, Type: "approval.expired", ProjectID: entry.ProjectID, Timestamp: old}
		if _, err := s.RecordNoticeWithEvent(ctx, entry, event); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := s.AdmitNotice(ctx, "chat", id, "large duplicated payload", old); err != nil {
				t.Fatal(err)
			}
			if err := s.AckNotice(ctx, id, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	newest, err := s.ListNotices(ctx, "proj-approval", "run", 1)
	if err != nil || len(newest) != 1 || newest[0].EventID != "notice-2" {
		t.Fatalf("newest: %+v %v", newest, err)
	}
	inbox, err := s.ListAdmittedNotices(ctx, "chat")
	if err != nil || len(inbox) != 1 || inbox[0].RenderedText != "" {
		t.Fatalf("inbox: %+v %v", inbox, err)
	}
	if err := s.PruneNotices(ctx, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	remaining, err := s.ListNotices(ctx, "proj-approval", "run", 10)
	if err != nil || len(remaining) != 2 {
		t.Fatalf("retained pending notices: %+v %v", remaining, err)
	}
	inbox, err = s.ListAdmittedNotices(ctx, "chat")
	if err != nil || len(inbox) != 0 {
		t.Fatalf("pruned inbox: %+v %v", inbox, err)
	}
	created, err := s.RecordNoticeWithEvent(ctx, NoticeOutboxEntry{EventID: "notice-0", ProjectID: "proj-approval", RunID: "run", Category: "approval", Transition: "expired", CreatedAt: old}, model.Event{ID: "notice-0", Type: "approval.expired", ProjectID: "proj-approval", Timestamp: old})
	if err != nil || created {
		t.Fatalf("recreated pruned event: %v %v", created, err)
	}
}
