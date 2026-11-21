package tui

import (
	"context"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// retryNoTaskWhy is the answer when no task is stuck.
const retryNoTaskWhy = "No task is stuck: nothing to retry."

// retryTarget is the stuck task the home screen's y (and the Web Board's
// Retry) would retry: the first parked (stuck) task of the run with no
// decision waiting for it and not already moved to another model. With
// none it is "" and one plain sentence saying why.
func retryTarget(w *Workspace) (string, string) {
	if !w.boardVisible() || w.anyBodyView() {
		return "", retryNoTaskWhy
	}
	p := w.marshalPanel()
	if p == nil {
		return "", "There is no run: nothing to retry."
	}
	inbox := w.boardInbox()
	why := retryNoTaskWhy
	for _, row := range p.Board.Tasks {
		if row.State != boardmodel.TaskParked {
			continue
		}
		id, cid := row.ID, cleanBoard(row.ID)
		if slices.ContainsFunc(inbox, func(it boardmodel.InboxItem) bool { return it.Request.TaskID == id }) {
			why = cid + " has a decision waiting: answer it on the Web Board (w)."
			continue
		}
		if canonicalTaskState(p, id) == marshal.Reassigned {
			why = cid + " already moved to another model; resume the run to start it."
			continue
		}
		return id, ""
	}
	return "", why
}

// canonicalTaskState is id's state in the panel's run snapshot, "" when the
// snapshot has no such task.
func canonicalTaskState(p *MarshalPanel, id string) marshal.TaskState {
	for _, t := range p.Tasks {
		if t.ID == id {
			return t.State
		}
	}
	return ""
}

// taskRow is the task row id names on the board; ok is false when the board
// has no such task.
func taskRow(b boardmodel.Board, id string) (boardmodel.TaskRow, bool) {
	for _, t := range b.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return boardmodel.TaskRow{}, false
}

// retryArmStatus is the footer line while r has armed a stuck task's retry.
func retryArmStatus(b boardmodel.Board, id string) string {
	title := ""
	if row, ok := taskRow(b, id); ok {
		title = row.Title
	}
	return "Retry " + cleanBoard(id) + " “" + cleanBoard(title) + "” in a fresh session? Enter retries · any other key cancels"
}

// handleRetryKey takes the next key once r has armed a stuck task's retry:
// Enter retries it in a fresh session and continues the run, any other key
// cancels. It takes nothing while nothing is armed.
func (w *Workspace) handleRetryKey(event KeyEvent) bool {
	id := w.boardUI.retryArmed
	if id == "" {
		return false
	}
	if event.Type == KeyEnter {
		w.boardUI.retryArmed = ""
		w.retryAndContinue(context.Background(), id)
		return true
	}
	w.disarmRetry()
	return true
}

// armRetry arms id's retry; the footer then asks for Enter and the next key
// runs it or cancels (handleRetryKey).
func (w *Workspace) armRetry(id string) {
	w.boardUI.retryArmed = id
	w.requestRepaint()
}

// disarmRetry cancels an armed retry and says nothing changed.
func (w *Workspace) disarmRetry() {
	w.boardUI.retryArmed = ""
	w.setOutput("Retry cancelled; nothing changed.", false)
}

// retryContinuation is the operator's answer once the retry succeeded, and the
// resume it runs when the run no longer waits on the operator. It is the
// production half the seam keeps so a test exercises it.
func retryContinuation(w *Workspace, id string, resume func() error) (string, error) {
	answer := cleanBoard(id) + " starts again in a fresh session."
	if w.marshalPanel().State != marshal.AwaitingUser {
		if err := resume(); err != nil {
			// The retry happened; only the resume failed. Say so rather
			// than reporting the retry as failed.
			w.RecordActivity("resume after retrying " + cleanBoard(id) + " failed: " + err.Error())
			return answer + " The run is still paused: resume it from the stop notice.", nil
		}
		return answer + " The run continues.", nil
	}
	return answer + " Other stuck tasks still hold the run.", nil
}

// boardRetry retries id in a fresh session and, when no other stuck task still
// holds the run, resumes it, returning the operator's answer. It is a package
// var so a test can stand in for the Marshal calls it makes.
var boardRetry = func(w *Workspace, ctx context.Context, id string) (string, error) {
	if _, err := w.marshalRetry(ctx, []string{id, "fresh-session"}); err != nil {
		return "", err
	}
	return retryContinuation(w, id, func() error { _, err := w.marshalResume(ctx); return err })
}

// retryAndContinue runs the retry on the ordinary command lane, with the same
// single-in-flight and cancellation rules a typed command gets, and shows a
// plain answer.
func (w *Workspace) retryAndContinue(ctx context.Context, id string) {
	w.runCommandFunc(ctx, "retry "+id, func(ctx context.Context) (string, error) {
		answer, err := boardRetry(w, ctx, id)
		if err != nil {
			return w.plainRetryError(id, err), nil
		}
		return answer, nil
	})
}

// plainRetryError turns a RetryTask service error into one plain sentence; the
// raw error is logged to Activity only for the ones with nothing to tell the
// operator.
func (w *Workspace) plainRetryError(id string, err error) string {
	msg := err.Error()
	id = cleanBoard(id)
	switch {
	case strings.Contains(msg, "a Marshal operation is already running"):
		return "The Marshal is in the middle of a step; try again when it finishes."
	case strings.Contains(msg, "maximum fresh session retries exceeded"):
		return id + " has been retried as often as allowed. Press w to give it to another model."
	case strings.Contains(msg, "cannot retry task with escalation reason:"):
		_, reason, _ := strings.Cut(msg, "cannot retry task with escalation reason:")
		return id + " cannot be retried: " + cleanBoard(strings.TrimSpace(reason)) + "."
	case strings.Contains(msg, "/marshal retry applies to returned or escalated tasks"):
		return id + " is not waiting for a retry; resume the run to continue it."
	case strings.Contains(msg, "unfinished lifecycle operation"):
		return "The run is finishing an earlier step; try again in a moment."
	default:
		w.RecordActivity("retry " + id + " failed: " + msg)
		return id + " could not be retried right now."
	}
}
