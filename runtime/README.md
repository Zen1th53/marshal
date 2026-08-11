# Runtime Plane

This directory defines the executable control-plane contracts for the reusable
agent engineering system.

It is a specification layer, not a claim that a production daemon is already
implemented.

## Components

- `AGENTCTL.md` — CLI/TUI command contract.
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

## Recommended First Implementation

```text
agentctl
  ↓
local daemon
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
  └── content-addressed artifacts
```

Add TurboVec, Cognee, Deja Vu, external secret managers, or distributed workers
only after a real requirement appears.

---

## V6 Protocol Boundaries

- remote agent collaboration: A2A `1.0`,
- MCP profile: `2026-07-28`,
- runtime remote coordination: `runtime/REMOTE-AGENTS.md`,
- shared-service isolation: `runtime/MULTI-TENANCY.md`.

Protocol profiles are negotiated/probed; they are not inferred from agent name.
