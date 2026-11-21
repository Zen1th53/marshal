package execution

import "errors"

// ErrHandInNotRecorded marks a failure after the worker's own session
// succeeded: MARSHAL could not record what it left (commit or reconcile the
// worktree). Re-running the worker cannot fix it and spends another model
// run, so callers stop for the owner instead of retrying.
var ErrHandInNotRecorded = errors.New("the worker finished but MARSHAL could not record its hand-in")
