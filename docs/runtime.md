# Runtime modes

## File-first mode

Repository policy and protocol files can guide agents without starting a
runtime. This mode does not provide transactional concurrency, provider
sandboxing, or runtime evidence by itself.

## Local Community runtime

MARSHAL v1.0.1 runs a project-local Go daemon over a mode-`0600` Unix socket.
Canonical state is SQLite schema v72 under the mode-`0700` `.marshal/`
directory. The same runtime services are used by CLI, MCP, A2A, and supported
live Web handlers.

Implemented behavior includes:

- deterministic forward migrations and transactional writes;
- task claims, sessions, heartbeats, leases, and startup reconciliation;
- role/capability/policy/risk gates;
- task-scoped Git worktrees and supervised provider processes;
- Codex, OpenCode, Gemini, and Claude adapters;
- sanitized, digest-addressed artifacts and durable events;
- canonical task-start memory recall and completion capture;
- backup creation, integrity verification, and offline restore; and
- bounded Community Resource Awareness.

The worker lifecycle is `REGISTER → ASSIGN → PREPARE → RUN → HEARTBEAT →
CHECKPOINT → VERIFY → RELEASE → EXIT`. A crash cannot mark a task complete.

## Sandbox honesty

Git worktrees are not security sandboxes. Bubblewrap is the strong Linux
backend. Missing required isolation fails closed. Process-only execution is an
explicit opt-in limited to eligible R0/R1 work; it is never silently selected
for R2/R3 work.

## Community boundary

Resource measurements and recommendations are read-only. Community does not
include an adaptive resource governor, fleet-wide placement, automatic model
migration, or continuous dynamic concurrency/context tuning. Multi-host
coordination and remote artifact stores are not part of v1.0.1.
