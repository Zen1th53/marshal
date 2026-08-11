# Runtime Implementation Roadmap

## Phase 1 — Local Control Plane

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

Add:
- worker spawn/terminate,
- heartbeat,
- task-scoped environment,
- evidence capture,
- sandbox limits.

## Phase 3 — Events and Automation

Add:
- transactional outbox,
- scheduler wakeups,
- QA/AppSec trigger events,
- verification invalidation on HEAD change.

## Phase 4 — Secrets

Add a local secret backend first.

Then adapters only if required.

## Phase 5 — Retrieval

Add in this order only as real needs appear:

```text
exact/lexical
→ TurboVec semantic
→ Deja Vu session history
→ Cognee graph relationships
```

## Phase 6 — Multi-host

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
