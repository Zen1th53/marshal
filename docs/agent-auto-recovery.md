# Agent Auto-Recovery (T14)

Provides automated checkpoint-based recovery, retry budget enforcement, and orphan process resumption.

## APIs

- `Recover(ctx, taskID, checkpointID)` -> `Plan`
