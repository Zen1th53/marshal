# Runtime Plane

This directory defines the executable control-plane contracts for the reusable
agent engineering system. Runtime Milestone 1 implements a local Linux subset
in `cmd/marshal` and `internal/`; contracts outside that subset remain
specifications.

## Components

- `MARSHAL-CLI.md` — CLI/TUI command contract.
- `MEMORY-SERVICE.md` — canonical state service.
- `IDENTITY-REGISTRY.md` — agent/session identity and heartbeat.
- `POLICY-ENGINE.md` — runtime authorization.
- `SANDBOX.md` — isolated worker execution.
- `SCHEDULER.md` — task dispatch and leases.
- `EVENT-BUS.md` — event semantics.
- `SECRETS-BROKER.md` — scoped secret leasing.
- `ARTIFACT-STORE.md` — immutable artifact storage.
- `WORKER-PROTOCOL.md` — worker lifecycle contract.
- `HEALTH.md` — health/readiness semantics.
- `THREAT-MODEL.md` — runtime threat model.
- `SCHEMA.yaml` — canonical runtime entities.
- `EVENTS.yaml` — stable event names and fields.
- `IMPLEMENTATION-ROADMAP.md` — recommended build order.

## Implemented Local Runtime 0.1.0

```text
marshal CLI
  ↓
HTTP/JSON over a local mode-0600 Unix socket
  ↓
SQLite
  ├── tasks
  ├── agents
  ├── leases
  ├── decisions
  ├── findings
  ├── approvals
  ├── artifacts
  └── audit_events

filesystem
  ├── Git worktrees
  └── SHA-256 content-addressed artifacts
```

The implementation also includes capability-policy enforcement, contextual
approvals, identity sessions/heartbeats, atomic leases, deterministic ready-task
ordering, Codex process management, bubblewrap probing/enforcement, durable
events, worker evidence, HEAD-change verification invalidation, doctor probes,
and read-only reconciliation diffs.

Bubblewrap is the strong Linux backend. If it is unavailable, only explicitly
low-risk, network-allowed work may use the honestly reported `process_only`
fallback; network-denied or R2/R3 execution is blocked.

Not implemented in 0.1.0: distributed or multi-host coordination, production
secret brokering, external event/artifact services, full MCP/A2A servers, and
production adapters other than Codex. TurboVec, Cognee, Deja Vu, external
secret managers, and distributed workers remain optional future evolution.

---

## V6 Protocol Boundaries

- remote agent collaboration: A2A `1.0`,
- MCP profile: `2026-07-28`,
- runtime remote coordination: `runtime/REMOTE-AGENTS.md`,
- shared-service isolation: `runtime/MULTI-TENANCY.md`.

Protocol profiles are negotiated/probed; they are not inferred from agent name.
