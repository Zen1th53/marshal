# Runtime Modes

## File-first mode

The role, protocol, memory, and template files can govern agent work without a
runtime service. Markdown is human-readable coordination state. This mode does
not provide transactional concurrency or process enforcement by itself.

## Local runtime mode

The runtime is implemented in Go. A local daemon exposes HTTP/JSON over a
mode-`0600` Unix socket and stores canonical live coordination state in SQLite.
The current MARSHAL schema is version 67. The project-local `.marshal/`
directory contains the database, socket, logs, artifacts, and task worktrees
and is excluded from Git.

Implemented behavior includes:

- deterministic migrations and transactional writes;
- atomic task claims, sessions, heartbeats, and leases;
- semantic policy decisions and contextual approvals;
- task-scoped Git worktrees and worker process management;
- Codex execution, durable events, digest-addressed artifacts, and evidence;
- verification invalidation when repository HEAD changes;
- read-only file/runtime reconciliation inspection.

The worker lifecycle is `REGISTER → ASSIGN → PREPARE → RUN → HEARTBEAT →
CHECKPOINT → VERIFY → RELEASE → EXIT`. A crash preserves worktree and evidence
and cannot mark a task complete.

## Sandbox honesty

Git worktrees isolate modifications; they are not a security sandbox.
Bubblewrap is the strong Runtime V1 Linux backend. When it is unavailable, the
runtime reports `process_only` and permits fallback only for eligible low-risk,
network-allowed tasks. It blocks network-denied and R2/R3 execution rather than
silently running unrestricted.

## Events and audit

Canonical state transitions and durable audit events share the SQLite
transaction boundary. Events contain operational metadata, not hidden model
reasoning. Artifacts bind bytes to digests, tasks, sessions, and source commits.

## Deployment boundary

The current store and scheduler are local to one control plane. MCP and A2A
provide authenticated remote entry points; they do not turn SQLite into a
distributed consensus system. No PostgreSQL, Redis, message broker, or
multi-host consensus dependency is part of this release.

Canonical references:

- [Policy-as-Code](policy-as-code.md)

- [runtime/README.md](../runtime/README.md)
- [runtime/ARCHITECTURE.md](../runtime/ARCHITECTURE.md)
- [runtime/IMPLEMENTATION-ROADMAP.md](../runtime/IMPLEMENTATION-ROADMAP.md)
- [runtime/SANDBOX.md](../runtime/SANDBOX.md)
- [runtime/EVENT-BUS.md](../runtime/EVENT-BUS.md)
