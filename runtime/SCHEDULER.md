# Task Scheduler

## Mission

Dispatch only work that is ready, owned coherently, and safe to execute.

## Ready Predicate

A task is dispatchable when:

```text
status == ready
AND hard dependencies satisfied
AND no active conflicting lease
AND required architecture/security pre-gates satisfied
AND required approval available or not yet needed
```

## Selection

Prefer:
- dependency-unblocking work,
- critical blockers,
- tasks whose prerequisites are complete,
- operator priority.

Do not optimize purely for maximum parallelism.

## Lease

Scheduler creates an atomic lease with:
- task,
- agent/session,
- branch/worktree,
- expiry,
- revision.

## Reclaim

Lease expiry is a signal.

Before reclaim:
- inspect heartbeat,
- inspect worktree,
- inspect checkpoint/handoff,
- inspect recent commits.

## Retry

Scheduler does not blindly retry failed tasks.

A retry requires:
- classified failure,
- changed condition or retry-safe transient cause,
- bounded attempt count.

## Deadlock

Integrate `protocols/LIVENESS.md`.

Dependency cycles or repeated role ping-pong require explicit resolution.
