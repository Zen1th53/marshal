# Multi-Agent Scheduler (T03)

Assigns tasks to optimal worker agents based on capability matching, current load, and lease management.

## APIs

- `Next(ctx, task, candidates)` -> `Assignment`
- `Renew(ctx, leaseID)`
- `Release(ctx, leaseID, outcome)`
