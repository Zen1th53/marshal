package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
)

const runNoticeSecret = "sk-ant-api03-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef0123456789"

func approvedNoticeRun(t *testing.T) *MarshalService {
	t.Helper()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	return s
}

func runNotices(t *testing.T, s *MarshalService) []store.NoticeOutboxEntry {
	t.Helper()
	notices, err := s.Store.ListNotices(t.Context(), s.ProjectID, "run", 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.NoticeOutboxEntry
	for _, n := range notices {
		if n.Category == RunNoticeCategory {
			out = append(out, n)
		}
	}
	return out
}

// A review that fails for a transport reason pauses the run at the review
// stage with the attempt kept, the raw cause bounded and redacted in the
// pause detail, and one durable run notice with the same reason.
func TestExecutionFailurePersistsStagePause(t *testing.T) {
	s := approvedNoticeRun(t)
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{}, "", errors.New("review transport: connection reset; key " + runNoticeSecret)
	}
	run, err := s.Execute(t.Context(), "run", marshalBrief, nil)
	if err == nil {
		t.Fatalf("execute succeeded: %+v", run)
	}
	stored, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	p := stored.Pause
	if stored.State != marshal.AwaitingUser || p == nil || p.Code != marshal.CodeReviewFailed || p.Action != marshal.PauseResume || p.ResumeState == "" || p.NoticeID == "" {
		t.Fatalf("pause: state %s %+v", stored.State, p)
	}
	if strings.Contains(p.Detail, runNoticeSecret) || !strings.Contains(p.Detail, "connection reset") {
		t.Fatalf("detail not redacted or missing cause: %q", p.Detail)
	}
	if stored.Tasks[0].State != marshal.HandedIn {
		t.Fatalf("attempt not kept: task %s", stored.Tasks[0].State)
	}
	notices := runNotices(t, s)
	if len(notices) != 1 || notices[0].EventID != p.NoticeID || notices[0].Summary != p.Reason || notices[0].Transition != RunNoticePaused {
		t.Fatalf("notices %+v", notices)
	}
	var payload RunNotice
	if err := json.Unmarshal([]byte(notices[0].PayloadJSON), &payload); err != nil || payload.RunID != "run" || payload.Code != marshal.CodeReviewFailed {
		t.Fatalf("payload %+v %v", payload, err)
	}
	// Saving the paused run again, even from a stale copy, does not repeat it.
	rec, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "run")
	if err != nil {
		t.Fatal(err)
	}
	stale := rec.Value
	stale.Pause.NoticeID = ""
	if err := s.save(t.Context(), "run", stale, rec.Revision); err != nil {
		t.Fatal(err)
	}
	if got := runNotices(t, s); len(got) != 1 {
		t.Fatalf("re-save repeated the notice: %+v", got)
	}
}

// Execute and Resume failures: recoverable stage failures pause with their
// own code, the person's cancel and an existing safety pause are left as
// they are, and a resume that fails keeps the pause and records one notice
// per distinct cause.
func TestExecutionAndResumePauseMatrix(t *testing.T) {
	t.Run("review failure pauses", func(t *testing.T) {
		s := approvedNoticeRun(t)
		s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
			return marshal.Review{}, "", errors.New("review admission refused")
		}
		_, _ = s.Execute(t.Context(), "run", marshalBrief, nil)
		run, _ := s.Snapshot(t.Context(), "run")
		if run.Pause == nil || run.Pause.Code != marshal.CodeReviewFailed || run.Pause.TaskID != "a" {
			t.Fatalf("pause %+v", run.Pause)
		}
	})
	t.Run("owner cancel is not a failure", func(t *testing.T) {
		s := approvedNoticeRun(t)
		ctx, cancel := context.WithCancel(t.Context())
		s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
			cancel()
			return marshal.Review{}, "", context.Canceled
		}
		_, err := s.Execute(ctx, "run", marshalBrief, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
		run, _ := s.Snapshot(t.Context(), "run")
		if run.State == marshal.AwaitingUser || run.Pause != nil || len(runNotices(t, s)) != 0 {
			t.Fatalf("cancel became a pause: %s %+v", run.State, run.Pause)
		}
	})
	t.Run("safety pause kept", func(t *testing.T) {
		s := approvedNoticeRun(t)
		rec, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "run")
		if err != nil {
			t.Fatal(err)
		}
		run := rec.Value
		pauseMarshal(&run, marshal.Pause{Reason: "security hold", Code: marshal.CodeSecurity, Resolutions: []string{"inspect"}})
		if err := s.save(t.Context(), "run", run, rec.Revision); err != nil {
			t.Fatal(err)
		}
		cause := errors.New("dispatch launch failed")
		if got := s.pauseStageFailure(t.Context(), "run", "dispatch", cause); !errors.Is(got, cause) {
			t.Fatalf("cause changed: %v", got)
		}
		after, _ := s.Snapshot(t.Context(), "run")
		if after.Pause == nil || after.Pause.Code != marshal.CodeSecurity {
			t.Fatalf("safety pause overwritten: %+v", after.Pause)
		}
	})
	for _, c := range []struct{ stage, code string }{{"dispatch", marshal.CodeDispatchFailed}, {"merge", marshal.CodeMergeFailed}, {"commands", marshal.CodeCommandsBlocked}} {
		t.Run(c.stage+" failure pauses", func(t *testing.T) {
			s := approvedNoticeRun(t)
			before, _ := s.Snapshot(t.Context(), "run")
			_ = s.pauseStageFailure(t.Context(), "run", c.stage, errors.New(c.stage+" broke"))
			run, _ := s.Snapshot(t.Context(), "run")
			if run.Pause == nil || run.Pause.Code != c.code || run.Pause.ResumeState != before.State || !strings.Contains(run.Pause.Detail, c.stage+" broke") {
				t.Fatalf("pause %+v", run.Pause)
			}
		})
	}
	t.Run("resume failure keeps pause and notifies once", func(t *testing.T) {
		s := approvedNoticeRun(t)
		rec, err := s.Store.GetMarshalRun(t.Context(), s.ProjectID, "run")
		if err != nil {
			t.Fatal(err)
		}
		run := rec.Value
		// No resume state: the pause must be resolved before resuming.
		pauseMarshal(&run, marshal.Pause{Reason: "task cancelled", Code: marshal.CodeTaskCancelled, Action: marshal.PauseUsePlan, Resolutions: []string{"approve an amended plan"}})
		if err := s.save(t.Context(), "run", run, rec.Revision); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := s.Resume(t.Context(), "run"); err == nil {
				t.Fatal("resume succeeded")
			}
		}
		after, _ := s.Snapshot(t.Context(), "run")
		if after.Pause == nil || after.Pause.Code != marshal.CodeTaskCancelled {
			t.Fatalf("pause replaced: %+v", after.Pause)
		}
		var failed []store.NoticeOutboxEntry
		for _, n := range runNotices(t, s) {
			if n.Transition == RunNoticeResumeFailed {
				failed = append(failed, n)
			}
		}
		if len(failed) != 1 || !strings.Contains(failed[0].Summary, "task cancelled") {
			t.Fatalf("resume notices %+v", failed)
		}
	})
}
