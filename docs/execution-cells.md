# Execution cells

Execution cells bind a task to one validated workspace and backend. The
canonical manager is `internal/cell.Manager`; callers must use its `Prepare`,
`Exec`, and `Destroy` methods rather than invoking a backend directly.

## Operator-visible behavior

Preparation validates the task ID, absolute workspace, backend, capabilities,
network profile, resource values, and secret references before persistence. A
cell starts in `new`, obtains one durable preparation claim, and reaches
`ready` only after the selected backend returns a handle matching the exact
task, backend, and workspace. Preparation claims are recovered by the durable
store CAS path; a second manager reconciles the canonical state instead of
starting another preparation.

An authorization failure, invalid scope, unavailable backend, or failed
preparation returns a stable typed cell error. There is no silent fallback
from Bubblewrap to the native backend. A failed cleanup transitions the cell
to `failed`; a repeated destroy of an already destroyed cell is idempotent.

## API example

```go
record, err := runtime.PrepareCell(ctx, cell.Spec{
    TaskID:    "TASK-example",
    Workspace: "/tmp/marshal-task",
    Backend:   cell.BackendBubblewrap,
})
if err != nil {
    // Handle the typed reason; do not parse the error string.
}
```

Successful preparation returns a `ready` record. A request with workspace
`/tmp/../outside` returns `CELL_SCOPE_ESCAPE` and does not call the backend.

## Security and backend limits

The manager validates and binds the workspace, task, backend, and specification
digest. The authorizer and secret broker remain separate prerequisites;
fixture text, provider labels, and metric values are not authority. Backend
process creation occurs outside the store transaction. The native backend is
not a secure isolation boundary by itself. Bubblewrap availability and the
host kernel's sandbox support are deployment prerequisites, not reasons to
silently select another backend.

## Metrics and events

When constructed with `NewObservedManager`, the manager records the bounded
`cell` operation in the shared `evidence.MetricsRecorder`: success, denied,
invalid, error, and cancellation results, total duration, active preparation
claims, and the last closed reason code. Metrics are in-process projections;
they are not persisted and cannot authorize, finalize, or alter a cell.

Audited managers emit `cell.prepare.started`, `cell.ready`,
`cell.exec.started`, `cell.exec.finished`, `cell.destroy.started`,
`cell.destroyed`, and `cell.failed`. Events contain bounded IDs, result
reasons, and the specification digest; raw command output and secret values
are not event authority.

## Recovery and reconciliation

The durable `execution_cells` row and its conditional preparation update are
the source of truth. A competing request waits for or reconciles the existing
claim. Reopening the store preserves the state and does not infer readiness
from an unrelated field. A stale or failed claim is not treated as a success;
callers receive a typed failure or the canonical ready record.
