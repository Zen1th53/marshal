# Runtime Architecture

## Component Boundaries

### marshal

Human/operator entrypoint.

Responsibilities:
- inspect state,
- start/stop local runtime,
- claim/release tasks,
- inspect agents,
- create handoffs/checkpoints,
- request/inspect approvals,
- inspect artifacts,
- trigger validation.

Does not directly mutate protected state without runtime policy.

### Runtime API / MCP

Stable client boundary for:
- agents,
- CLI,
- integrations.

Must enforce authentication/identity and policy.

### Canonical Store

Authoritative structured state.

Recommended local implementation:
- SQLite,
- transactions,
- foreign keys,
- WAL if appropriate.

### Scheduler

Determines which ready tasks may be dispatched.

Does not bypass task dependencies or approval gates.

### Policy Engine

Answers:

```text
subject
+ role
+ task
+ operation
+ target
+ environment
+ capability
+ approval
→ allow | deny | require_approval
```

### Worker Manager

Creates/monitors task-scoped workers.

### Sandbox Adapter

Creates execution isolation:
- worktree/path scope,
- environment,
- network rules,
- resource limits,
- secret injection.

### Event Bus

Distributes state transitions.

Canonical state transition should not depend on best-effort ephemeral events.

### Secrets Broker

Issues scoped credentials without storing them in general memory.

### Artifact Store

Stores immutable outputs addressed by digest.

### Retrieval Adapters

Derived lookup:
- exact/lexical,
- TurboVec-style semantic,
- Cognee-style graph,
- Deja Vu-style episodic.

No retrieval adapter owns canonical state.

## Transaction Boundary

For a critical state change:

```text
validate
→ policy
→ canonical transaction
→ durable event/outbox
→ commit
→ publish
```

Prefer transactional outbox semantics over "DB write succeeded but event vanished."

## Runtime IDs

Stable IDs:

```text
AGENT-
SESSION-
TASK-
LEASE-
EVENT-
ART-
APR-
FIND-
DEC-
CHK-
RUN-
```

## Time

Store timestamps in UTC internally.

Display in operator locale.

Lease semantics must tolerate clock skew if multi-host mode is introduced.
