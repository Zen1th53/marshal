package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// A worker that has waited two minutes on its model is announced once: on
// both Boards at once, and in the Marshal's chat, which gets it when it is
// attached and again, still once, after it reattaches.
func TestModelWaitNoticeReachesMarshalAndBoardsAcrossReconnect(t *testing.T) {
	w, rt := receiptTestWorkspace(t, "SESSION-model-wait")
	runID := "RUN-model-wait"
	seedMarshalRunForReceiptTest(t, w, runID, marshal.Run{
		State: marshal.Dispatching,
		Tasks: []marshal.Task{{PlanTaskID: "T-1", Title: "Task", Worker: "codex", State: marshal.Dispatched}},
	})
	var mu sync.Mutex
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	oldClock := boardNow
	boardNow = clock
	t.Cleanup(func() { boardNow = oldClock })
	beat, signal, end := rt.TrackWorkerForTest(runID, "T-1", "codex", clock)
	defer end()
	beat()
	signal(adapter.SignalRequestStart)
	advance(time.Minute)
	beat()
	w.signalModelWaits(t.Context())
	if inbox := readMarshalInboxText(t, w); strings.Contains(inbox, "model_wait") {
		t.Fatalf("announced before two minutes:\n%s", inbox)
	}
	advance(app.ModelWaitNoticeAfter)
	beat()

	// Both Boards show the wait.
	w.refreshBoard(t.Context())
	lanes := w.boardV2().Lanes.Items
	if len(lanes) != 1 || lanes[0].Step != "waiting on model" || lanes[0].Process != app.ProcessLive || lanes[0].Turn != app.TurnWorking ||
		lanes[0].ModelWait < app.ModelWaitNoticeAfter || !strings.Contains(lanes[0].Note, "waiting on its model") {
		t.Fatalf("TUI Board lanes %+v", lanes)
	}
	v := boardVisual{board: w.marshalPanel().Board, now: clock()}
	if got := v.quietNote("running"); !strings.Contains(got, "worker on T-1: waiting on its model") {
		t.Fatalf("TUI Board footer %q", got)
	}
	frame := webboard.FromBoard(w.boardV2())
	if got := frame.Lanes.Items; len(got) != 1 || got[0].Process != app.ProcessLive || got[0].ModelWait < int64(app.ModelWaitNoticeAfter/time.Second) || got[0].Note == "" {
		t.Fatalf("web Board lanes %+v", got)
	}

	// The attached chat gets the notice once, however often the Board looks.
	for range 3 {
		w.signalModelWaits(t.Context())
	}
	inbox := readMarshalInboxText(t, w)
	if got := strings.Count(inbox, "## MARSHAL run model_wait · "+runID); got != 1 {
		t.Fatalf("attached chat got %d notices:\n%s", got, inbox)
	}
	if !strings.Contains(inbox, "task T-1") || !strings.Contains(inbox, "Next: ") {
		t.Fatalf("notice text:\n%s", inbox)
	}

	// The model answers; a new wait goes on while the chat is closed and its
	// inbox cannot be written.
	signal(adapter.SignalNone)
	signal(adapter.SignalToolEnd)
	inboxDir := filepath.Join(rt.ProjectRoot(), statepath.DirName, "inbox")
	if err := os.RemoveAll(inboxDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inboxDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	advance(app.ModelWaitNoticeAfter + time.Second)
	beat()
	w.signalModelWaits(t.Context())
	pending, err := rt.Store().PendingNotices(t.Context(), rt.ProjectIdentity(), runID, 10)
	if err != nil || len(pending) != 1 || pending[0].Transition != app.RunNoticeModelWait {
		t.Fatalf("pending while detached: %+v %v", pending, err)
	}
	// The chat reattaches: the waiting notice arrives once, the first is
	// not repeated.
	if err := os.Remove(inboxDir); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w.replayMarshalNotices(t.Context())
		w.signalModelWaits(t.Context())
	}
	inbox = readMarshalInboxText(t, w)
	if got := strings.Count(inbox, "## MARSHAL run model_wait · "+runID); got != 1 {
		t.Fatalf("reattached chat got %d notices:\n%s", got, inbox)
	}
	if pending, _ := rt.Store().PendingNotices(t.Context(), rt.ProjectIdentity(), runID, 10); len(pending) != 0 {
		t.Fatalf("still pending: %+v", pending)
	}
}
