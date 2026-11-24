package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// An adapter limit refusal persisted through the durable notice outbox is
// delivered once to both Boards (Activity) and the Marshal chat inbox,
// redacted, and replays cleanly when the inbox becomes writable again.
func TestAdapterLimitNoticeDeliveredToBoardsAndMarshalChat(t *testing.T) {
	w, rt := receiptTestWorkspace(t, "SESSION-limit-notice")
	limit := model.Limit{
		Kind:        "design_record",
		Subject:     "design artifact secret=" + runNoticeTestSecret,
		ActualBytes: app.MaxDesignArtifactBytes + 1,
		CapBytes:    app.MaxDesignArtifactBytes,
		Action:      "Record a smaller artifact.",
	}

	// Simulate the Marshal chat inbox being temporarily unavailable.
	inboxDir := filepath.Join(rt.ProjectRoot(), statepath.DirName, "inbox")
	if err := os.RemoveAll(inboxDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inboxDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.recordAdapterLimitError(t.Context(), "", "design_record", model.NewLimitError(errors.New("too large"), limit))

	pending, err := rt.Store().PendingNotices(t.Context(), rt.ProjectIdentity(), "", 10)
	if err != nil || len(pending) != 1 || pending[0].Category != app.LimitNoticeCategory {
		t.Fatalf("pending after failed delivery: %+v err=%v", pending, err)
	}

	// Restore the inbox directory and replay twice; the notice must reach
	// both Boards (Activity) and Marshal chat exactly once.
	if err := os.Remove(inboxDir); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w.replayMarshalNotices(t.Context())
	}

	inbox := readMarshalInboxText(t, w)
	if got := strings.Count(inbox, "## MARSHAL limit reached · design_record"); got != 1 {
		t.Fatalf("marshal inbox limit notices = %d:\n%s", got, inbox)
	}
	if strings.Contains(inbox, runNoticeTestSecret) {
		t.Fatalf("secret leaked into marshal inbox:\n%s", inbox)
	}
	if !strings.Contains(inbox, "Record a smaller artifact.") {
		t.Fatalf("marshal inbox missing action:\n%s", inbox)
	}

	activity := w.boardV2().Activity
	foundActivity := false
	for _, ev := range activity {
		if strings.Contains(ev.Text, runNoticeTestSecret) {
			t.Fatalf("secret leaked into Board Activity: %+v", ev)
		}
		if strings.Contains(ev.Text, "Limit reached:") && strings.Contains(ev.Text, "design artifact") {
			foundActivity = true
		}
	}
	if !foundActivity {
		t.Fatalf("Board Activity missing limit notice: %+v", activity)
	}
	if left, err := rt.Store().PendingNotices(t.Context(), rt.ProjectIdentity(), "", 10); err != nil || len(left) != 0 {
		t.Fatalf("notice still pending after replay: %+v err=%v", left, err)
	}
}
