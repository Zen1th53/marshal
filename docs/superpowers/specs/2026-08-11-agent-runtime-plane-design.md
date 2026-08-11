# Agent Runtime Plane Design

## Context

The engineering pack already defines roles, doctrine, memory, task ownership,
worktree isolation, approvals, evidence, CI/CD, supply chain, data policy,
traceability, liveness, and pack migrations.

The remaining step is to define the executable runtime boundary that can enforce
those rules rather than relying only on model compliance.

## Goal

Define a backend-agnostic runtime architecture for:

- `agentctl` CLI/TUI entrypoint,
- canonical memory/control-plane service,
- agent identity registry and heartbeat,
- policy enforcement,
- isolated worker/sandbox execution,
- task scheduler,
- event bus,
- secrets broker,
- artifact store,
- worker protocol,
- runtime health/audit.

## Non-goals

- Ship a production cloud service in this pack.
- Require Kubernetes.
- Require Postgres when SQLite is enough.
- Require a vector database.
- Require one LLM vendor.
- Execute arbitrary untrusted code outside authorization.
- Replace repository-native CI or secret-management systems.

## Architecture

```text
                           USER
                            │
                         agentctl
                            │
                     Runtime API / MCP
                            │
        ┌───────────────────┼────────────────────┐
        │                   │                    │
   Identity Registry    Policy Engine       Task Scheduler
        │                   │                    │
        └──────────────┬────┴──────────────┬────┘
                       │                   │
                 Worker Manager       Event Bus
                       │                   │
                 Sandbox Adapter           │
                       │                   │
                  Agent Worker ◄───────────┘
                       │
        ┌──────────────┼──────────────┐
        │              │              │
  Canonical Store   Secrets Broker  Artifact Store
        │
   Retrieval Adapters
  lexical / TurboVec / Cognee / Deja Vu
```

## Core Invariants

- Policy check happens before privileged execution.
- Agent identity is explicit and session-scoped.
- Task lease is atomic.
- Worker execution is bound to task/branch/worktree.
- Heartbeat loss does not immediately imply safe takeover.
- Secrets are leased, scoped, short-lived where possible, and never stored in memory.
- Artifacts are immutable by digest.
- Events carry stable IDs and are replay-safe where required.
- Semantic/graph/history adapters are derived indexes, not canonical truth.
- Worker crash cannot silently mark a task complete.
- Verification binds to exact commit/artifact state.
- Production/destructive operations require approval in addition to capability.
- Runtime outage must fail closed for privileged operations.

## Runtime Modes

### Mode A — File-first local

```text
Markdown/YAML + Git + worktrees
```

Useful for:
- one machine,
- low concurrency,
- transparent debugging.

### Mode B — Local runtime

```text
agentctl
+ local daemon
+ SQLite
+ filesystem artifact store
+ local workers
```

Recommended first executable implementation.

### Mode C — Multi-host runtime

```text
API/MCP service
+ Postgres
+ distributed workers
+ external secret manager
+ object artifact store
+ event transport
```

Only when real multi-host concurrency requires it.

## Security Boundary

All retrieved instructions, issue text, web content, memory text, and external
reference material remain untrusted data. Policy and capability decisions come
from trusted runtime configuration and repository/owner policy.

## Failure Model

- Scheduler unavailable → no new task dispatch.
- Policy engine unavailable → privileged operation denied.
- Secrets broker unavailable → secret-dependent operation blocked.
- Semantic retrieval unavailable → canonical state still works.
- Event bus unavailable → canonical transaction completes only if required event
  durability can be guaranteed; otherwise fail rather than silently lose a
  critical transition.
- Worker dies → lease becomes suspect; task is not automatically complete.
- Artifact store unavailable → release/build artifact step blocks.
