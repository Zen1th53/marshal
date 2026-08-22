# Runtime Plane

This directory defines the executable control-plane contracts and implementation
for the MARSHAL agent runtime (`1.0.0`, SQLite schema `v70`).

## Components

- `MARSHAL-CLI.md` — CLI command contract.
- `MEMORY-SERVICE.md` — canonical state service.
- `IDENTITY-REGISTRY.md` — agent/session identity and heartbeat.
- `POLICY-ENGINE.md` — runtime authorization and capability broker.
- `SANDBOX.md` — isolated worker execution.
- `SCHEDULER.md` — task dispatch, scoring engine, and atomic leases.
- `EVENT-BUS.md` — append-only durable event semantics.
- `SECRETS-BROKER.md` — scoped secret leasing and redaction.
- `ARTIFACT-STORE.md` — content-addressed immutable artifact store.
- `WORKER-PROTOCOL.md` — worker lifecycle contract.
- `HEALTH.md` — health and readiness diagnostics.
- `THREAT-MODEL.md` — runtime threat model and security boundaries.
- `SCHEMA.yaml` — canonical runtime entities.
- `EVENTS.yaml` — stable event names and fields.
- `IMPLEMENTATION-ROADMAP.md` — implemented milestones and build order.

## Implemented Runtime Architecture (v1.0.0)

```text
marshal CLI / Local UI
  |
  +--> Mode-0600 Unix Domain Socket (Local Control Plane)
  +--> Loopback-only Bearer-token MCP Server (2026-07-28)
  +--> Loopback-only Bearer-token A2A Server (1.0)
  |
SQLite (WAL Mode, Schema v70)
  |-- tasks (Canonical Review -> QA -> Security -> Merge Lifecycle)
  |-- agents & sessions (Role Authorization & Heartbeats)
  |-- leases (Multi-factor Scheduler Scoring & TTL)
  |-- worker_runs & execution_cells
  |-- audit_events (Append-only Audit Log)
  |-- artifacts (Content-addressed sha256 reference tracking)
  +-- quorum_verifications & risk_assessments

Storage & Sandbox Execution
  |-- Git Worktrees (.marshal/worktrees/ with retention GC)
  |-- SHA-256 Content-Addressed Artifacts (.marshal/artifacts/ with GC)
  +-- Bubblewrap Sandbox (Unprivileged mount & network namespaces)
```

The runtime enforces:
- Fail-closed security boundaries across native CLI, MCP, and A2A interfaces.
- Fine-grained token capabilities (`task:run`, `task:claim`, `mcp:read`, `a2a:send`).
- Dynamic multi-factor scheduler scoring and profile-based model routing.
- Real recovery state machine with failure classification and backoff.
- Task merge gate with multi-party quorum verification.
- Content-addressed artifact reference tracking and GC.
- SQLite online backup, restore preflight, and startup orphan reconciliation.
- Bubblewrap process isolation with 500MB worktree disk budget and process group timeouts.

If bubblewrap is unavailable, only low-risk (R1) tasks with explicit network allowance
may use the process-only fallback; high-risk (R2/R3) or network-denied tasks fail closed.

Distributed multi-host clustering and cloud-native object stores remain future milestones.

---

## V6 Protocol Boundaries

- Remote agent collaboration: A2A `1.0` (loopback-bound, bearer-authorized).
- MCP profile: `2026-07-28` (stateless, capability-token protected).
- Multi-tenancy and remote coordination: see `runtime/REMOTE-AGENTS.md` and `runtime/MULTI-TENANCY.md`.
