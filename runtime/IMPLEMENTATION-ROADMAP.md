# Runtime Implementation Roadmap

## Phase 1 — Local Control Plane

Status: implemented in local runtime `0.1.0`.

Build only:

```text
agentctl
+ local daemon
+ SQLite
+ task/lease state
+ identity/session registry
+ policy checks
+ filesystem worktrees
+ local artifact store
```

This is the highest-value executable milestone.

## Phase 2 — Worker Manager

Status: implemented for task-scoped local Codex and deterministic test adapters.

Add:
- worker spawn/terminate,
- heartbeat,
- task-scoped environment,
- evidence capture,
- sandbox limits.

## Phase 3 — Events and Automation

Status: partially implemented in `0.1.0`.

Add:
- transactional outbox,
- scheduler wakeups,
- QA/AppSec trigger events,
- verification invalidation on HEAD change.

Durable transactional audit events and HEAD invalidation are implemented.
Background scheduler wakeups and automatic QA/AppSec workers are not.

## Phase 4 — Secrets

Status: not implemented.

Add a local secret backend first.

Then adapters only if required.

## Phase 5 — Retrieval

Status: not implemented.

Add in this order only as real needs appear:

```text
exact/lexical
→ TurboVec semantic
→ Deja Vu session history
→ Cognee graph relationships
```

## Phase 6 — Multi-host

Status: not implemented.

Only after single-host limits are measured.

Potential changes:
- Postgres,
- object artifact store,
- distributed workers,
- external event transport,
- centralized secrets.

## Torvalds Test

At every phase:

```text
Does this layer solve a current problem?
Can we delete a component?
Can SQLite/filesystem still do it?
Is the abstraction serving real callers?
Can the change be independently verified and reverted?
```
