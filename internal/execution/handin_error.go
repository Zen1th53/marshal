package execution

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// ErrHandInNotRecorded marks a failure after the worker's own session
// succeeded: MARSHAL could not record what it left (commit or reconcile the
// worktree). Re-running the worker cannot fix it and spends another model
// run, so callers stop for the owner instead of retrying.
var ErrHandInNotRecorded = errors.New("the worker finished but MARSHAL could not record its hand-in")

// stageHandIn is the RunFailure stage of a task whose worker succeeded but
// whose result could not be recorded. It is never "EXECUTE": that stage
// names a provider execution failure, which failover reads.
const stageHandIn = "HAND_IN"

func handInFailure(taskID, reason string) RunFailure {
	return RunFailure{TaskID: taskID, Stage: stageHandIn, Reason: reason, Timestamp: time.Now().UTC(), Recoverable: false}
}

// runFailed is the error of a run that ended with a failed task. It carries
// each failed task's own reason, so the cause reaches the owner instead of
// only "one or more tasks failed".
type runFailed struct {
	text        string
	afterWorker bool
}

func (e *runFailed) Error() string { return e.text }

func (e *runFailed) Is(target error) bool {
	return e.afterWorker && target == ErrHandInNotRecorded
}

func runFailedError(run ExecutionRun) error {
	ids := make([]string, 0, len(run.Tasks))
	for id, task := range run.Tasks {
		if task.State == TaskFailed {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var reasons []string
	// Only a run whose every failed task failed after its worker is marked:
	// a provider failure beside it still deserves its own retry.
	afterWorker := len(ids) > 0
	for _, id := range ids {
		task := run.Tasks[id]
		reason := strings.TrimSpace(task.LastFailureReason)
		if reason == "" {
			continue
		}
		if len(ids) > 1 {
			reason = id + ": " + reason
		}
		reasons = append(reasons, reason)
	}
	for _, id := range ids {
		task, handIn := run.Tasks[id], false
		for _, f := range run.Failures {
			if f.TaskID == task.TaskID && f.Stage == stageHandIn && f.Reason == task.LastFailureReason {
				handIn = true
			}
		}
		afterWorker = afterWorker && handIn
	}
	text := "run failed: one or more tasks failed"
	if len(reasons) > 0 {
		text += ": " + strings.Join(reasons, "; ")
	}
	return &runFailed{text: text, afterWorker: afterWorker}
}

// HandInNotRecorded marks err, a failure to record the hand-in of a worker
// that finished, with ErrHandInNotRecorded; its text is unchanged.
func HandInNotRecorded(err error) error {
	if err == nil || errors.Is(err, ErrHandInNotRecorded) {
		return err
	}
	return &handInNotRecorded{err}
}

type handInNotRecorded struct{ err error }

func (e *handInNotRecorded) Error() string        { return e.err.Error() }
func (e *handInNotRecorded) Unwrap() error        { return e.err }
func (e *handInNotRecorded) Is(target error) bool { return target == ErrHandInNotRecorded }
