package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
)

const runNoticeTestSecret = "sk-ant-api03-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef0123456789"

// A pause recorded while the Marshal's inbox could not be written is
// replayed when the chat reconnects: once, redacted, with the same reason
// the Board shows.
func TestPausedNoticeReplayedAfterChatReconnect(t *testing.T) {
	w, rt := receiptTestWorkspace(t, "SESSION-run-notice-replay")
	runID := "RUN-notice-replay"
	reason := "Marshal could not review the handed-in work (task T-1). key " + runNoticeTestSecret
	seedMarshalRunForReceiptTest(t, w, runID, marshal.Run{
		State: marshal.AwaitingUser,
		Pause: &marshal.Pause{Reason: reason, Code: marshal.CodeReviewFailed, Action: marshal.PauseResume, ResumeState: marshal.Reviewing, TaskID: "T-1", NoticeID: "run:" + runID + ":pause:1", Resolutions: []string{"Press Enter to try again (/marshal resume)."}},
		Tasks: []marshal.Task{{PlanTaskID: "T-1", Title: "Task", Worker: "codex", State: marshal.HandedIn}},
	})
	inboxDir := filepath.Join(rt.ProjectRoot(), statepath.DirName, "inbox")
	if err := os.RemoveAll(inboxDir); err != nil {
		t.Fatal(err)
	}
	// The chat is gone and its inbox cannot be written.
	if err := os.WriteFile(inboxDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.deliverApprovalNotices(t.Context(), runID)
	pending, err := rt.Store().PendingNotices(t.Context(), rt.ProjectIdentity(), runID, 10)
	if err != nil || len(pending) != 1 || pending[0].Category != app.RunNoticeCategory {
		t.Fatalf("pending after failed delivery: %+v %v", pending, err)
	}
	if err := os.Remove(inboxDir); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w.replayMarshalNotices(t.Context())
	}
	inbox := readMarshalInboxText(t, w)
	if got := strings.Count(inbox, "<!-- notice:run:"+runID+":pause:1 -->"); got != 1 {
		t.Fatalf("notice delivered %d times:\n%s", got, inbox)
	}
	if strings.Contains(inbox, runNoticeTestSecret) {
		t.Fatalf("secret reached the inbox:\n%s", inbox)
	}
	if !strings.Contains(inbox, "## MARSHAL run paused · "+runID) || !strings.Contains(inbox, "Marshal could not review the handed-in work (task T-1).") || !strings.Contains(inbox, "Next: Press Enter to try again") {
		t.Fatalf("notice text:\n%s", inbox)
	}
	board := w.marshalPanel().Board.Run
	if !strings.HasPrefix(board.Paused, "Marshal could not review the handed-in work (task T-1).") {
		t.Fatalf("Board reason %q", board.Paused)
	}
	if pending, _ := rt.Store().PendingNotices(t.Context(), rt.ProjectIdentity(), runID, 10); len(pending) != 0 {
		t.Fatalf("still pending: %+v", pending)
	}
}

// A resume that does not go through keeps the run's pause, shows the error
// on the Board, and reaches the Marshal's inbox as one run notice.
func TestResumeErrorNotifiesBoardAndMarshal(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "SESSION-run-notice-resume")
	runID := "RUN-notice-resume"
	service, _ := seedMarshalRunForReceiptTest(t, w, runID, marshal.Run{
		State: marshal.AwaitingUser,
		Pause: &marshal.Pause{Reason: "task T-1 cancelled; approved plan scope requires review", Code: marshal.CodeTaskCancelled, Action: marshal.PauseUsePlan, Resolutions: []string{"approve an amended plan, then /marshal use-plan"}},
		Tasks: []marshal.Task{{PlanTaskID: "T-1", Title: "Task", Worker: "codex", State: marshal.Cancelled}},
	})
	m := w.marshalSession()
	for range 2 {
		run, err := service.Resume(t.Context(), runID)
		if err == nil {
			t.Fatal("resume succeeded")
		}
		w.reportResumeFailure(t.Context(), m, runID, "codex", run, err)
	}
	panel := w.marshalPanel()
	if panel == nil || !strings.HasPrefix(panel.Note, "resume failed: ") {
		t.Fatalf("Board note %+v", panel)
	}
	run, err := service.Snapshot(t.Context(), runID)
	if err != nil || run.Pause == nil || run.Pause.Code != marshal.CodeTaskCancelled {
		t.Fatalf("pause not kept: %+v %v", run.Pause, err)
	}
	inbox := readMarshalInboxText(t, w)
	if got := strings.Count(inbox, "## MARSHAL run resume_failed · "+runID); got != 1 {
		t.Fatalf("resume notices %d:\n%s", got, inbox)
	}
	cause := strings.TrimPrefix(panel.Note, "resume failed: ")
	if words := strings.Fields(cause); len(words) < 2 || !strings.Contains(inbox, strings.Join(words[:2], " ")) {
		t.Fatalf("inbox does not carry the Board's cause %q:\n%s", cause, inbox)
	}
}

// The person's stop, a shutdown and a failure each end a Marshal step with
// their own outcome: only the failure is told to the Marshal as an error.
func TestOwnerCancelHasDistinctOutcome(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	t.Cleanup(w.Close)
	m := w.marshalSession()
	stopCause := func(stop func()) string {
		t.Helper()
		ctx, holder, cancel, err := m.reserveWithHolder(context.Background(), marshal.OperationHolder{Kind: "resume"})
		if err != nil {
			t.Fatal(err)
		}
		stop()
		if ctx.Err() == nil {
			t.Fatal("step not cancelled")
		}
		m.mu.Lock()
		cause := m.stopCause
		m.mu.Unlock()
		m.finishHolder(holder, cancel, false)
		return cause
	}
	owner := stopCause(func() { w.marshalStop() })
	shutdown := stopCause(func() { w.noteMarshalShutdown(); w.marshalStopFor(marshalStopShutdown) })
	if owner != marshalStopByOwner || shutdown != marshalStopShutdown {
		t.Fatalf("causes %q %q", owner, shutdown)
	}
	run := marshal.Run{State: marshal.Reviewing, PlanID: "PLAN-1"}
	ownerNote, ownerAlert := marshalExecuteOutcome(run, context.Canceled, true, owner)
	shutdownNote, shutdownAlert := marshalExecuteOutcome(run, context.Canceled, true, shutdown)
	failNote, failAlert := marshalExecuteOutcome(run, errors.New("worker launch failed"), false, "")
	if ownerAlert || shutdownAlert || !failAlert {
		t.Fatalf("alerts owner=%v shutdown=%v failure=%v", ownerAlert, shutdownAlert, failAlert)
	}
	if ownerNote == shutdownNote || ownerNote == failNote || shutdownNote == failNote {
		t.Fatalf("outcomes not distinct: %q %q %q", ownerNote, shutdownNote, failNote)
	}
	for note, want := range map[string]string{ownerNote: "You stopped the run", shutdownNote: "interrupted"} {
		reason, next, command, ok := parseKnownStopNote(note)
		if !ok || !strings.Contains(reason, want) || strings.Contains(reason, "unexpectedly") || next == "" || command != "/marshal resume" {
			t.Fatalf("note %q -> %q %q %q %v", note, reason, next, command, ok)
		}
	}
	// A failure whose pause was already announced is not alerted twice.
	paused := marshal.Run{State: marshal.AwaitingUser, Pause: &marshal.Pause{Reason: "x", NoticeID: "run:R:pause:1"}}
	if _, alert := marshalExecuteOutcome(paused, errors.New("review broke"), false, ""); alert {
		t.Fatal("announced pause alerted again")
	}
}
