//go:build linux

package tui

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Enter on an armed retry runs the retry and, when no other stuck task holds
// the run, the resume; otherwise only the retry, and the answer says so.
func TestBoardRetryRunsRetryThenResume(t *testing.T) {
	orig := boardRetry
	defer func() { boardRetry = orig }()
	for _, c := range []struct {
		name      string
		state     marshal.RunState
		wantCalls []string
		wantAns   string
	}{
		{"run continues", marshal.Dispatching, []string{"retry T-023 fresh-session", "resume"}, "T-023 starts again in a fresh session. The run continues."},
		{"others still hold", marshal.AwaitingUser, []string{"retry T-023 fresh-session"}, "T-023 starts again in a fresh session. Other stuck tasks still hold the run."},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, _ := boardWorkspace(t)
			defer w.Close()
			w.mu.Lock()
			w.state.Marshal.State = c.state
			w.mu.Unlock()
			var calls []string
			boardRetry = func(w *Workspace, ctx context.Context, id string) (string, error) {
				calls = append(calls, "retry "+id+" fresh-session")
				return retryContinuation(w, id, func() error { calls = append(calls, "resume"); return nil })
			}
			w.retryAndContinue(t.Context(), "T-023")
			w.commandWG.Wait()
			if !slices.Equal(calls, c.wantCalls) {
				t.Fatalf("calls %q, want %q", calls, c.wantCalls)
			}
			w.mu.RLock()
			ans := w.answer
			w.mu.RUnlock()
			if ans != c.wantAns {
				t.Fatalf("answer %q, want %q", ans, c.wantAns)
			}
		})
	}
}

// A retry that succeeds followed by a resume that fails is not reported as
// a failed retry: the answer says the task starts again and the run waits.
func TestBoardRetryResumeFailureSaysTheRunWaits(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	w.mu.Lock()
	w.state.Marshal.State = marshal.Dispatching
	w.mu.Unlock()
	ans, err := retryContinuation(w, "T-023", func() error { return errors.New("resume refused") })
	if err != nil {
		t.Fatalf("err %v, want an answer", err)
	}
	if want := "T-023 starts again in a fresh session. The run is still paused: resume it from the stop notice."; ans != want {
		t.Fatalf("answer %q, want %q", ans, want)
	}
}

// Each RetryTask service error becomes one plain sentence; the catch-all logs
// the raw error to Activity and tells the operator nothing of it.
func TestPlainRetryError(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"busy", errors.New("a Marshal operation is already running; /marshal stop first"), "The Marshal is in the middle of a step; try again when it finishes."},
		{"max retries", errors.New("maximum fresh session retries exceeded"), "T-023 has been retried as often as allowed. Press w to give it to another model."},
		{"permanent", errors.New("cannot retry task with escalation reason: rework limit reached"), "T-023 cannot be retried: rework limit reached."},
		{"unfinished", errors.New("unfinished lifecycle operation; /marshal resume to reconcile it"), "The run is finishing an earlier step; try again in a moment."},
		{"other", errors.New("boom: some raw error chain"), "T-023 could not be retried right now."},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := w.plainRetryError("T-023", c.err); got != c.want {
				t.Fatalf("plainRetryError = %q, want %q", got, c.want)
			}
		})
	}
}
