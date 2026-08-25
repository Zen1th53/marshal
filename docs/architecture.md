# MARSHAL architecture

MARSHAL separates durable engineering authority from the provider process that
performs work. This page describes the v1.0.1 Community runtime at SQLite
schema v72.

```text
CLI / Unix socket / MCP / A2A / loopback Web
                     |
                     v
                  Runtime
                     |
       capability broker + role authority
                     |
          risk gate + network decision
                     |
        worktree + Bubblewrap sandbox
                     |
          Codex / OpenCode / Gemini / Claude
                     |
         sanitized evidence + event log
                     |
        canonical SQLite state and memory
```

## Entry surfaces

The CLI can call the runtime directly or through the mode-`0600` local daemon
socket. MCP (`2026-07-28`) and A2A (`1.0`) are authenticated protocol entry
points into the same runtime. The Web UI is loopback-bound by default and uses
one-time codes, sessions, CSRF checks, CSP, and route authority checks.

## Execution path

`Runtime.Run` performs the canonical sequence:

1. load the task and validate its expected revision;
2. evaluate role, capability, risk, gate, and network policy;
3. prepare a task-scoped Git worktree;
4. recall bounded, authorized canonical memory;
5. resolve and probe the selected provider adapter;
6. choose an enforceable isolation boundary;
7. supervise the process with timeout and output bounds;
8. sanitize and persist evidence and Git observations; and
9. finalize runtime state and capture evidence-linked candidate memory.

Bubblewrap provides the strong Linux filesystem/process boundary. Endpoint
host/port rules are evaluated by policy, but Bubblewrap alone cannot enforce
them. Until an enforcing proxy is configured, endpoint-restricted egress is
rejected instead of broadened.

## Canonical state

SQLite in WAL mode is authoritative for projects, agents, sessions, tasks,
leases, runs, events, evidence references, policy state, handoffs, and memory.
Git worktrees isolate task modifications, and content-addressed artifacts bind
captured bytes to SHA-256 digests. Derived memory indexes can be rebuilt and do
not replace `memory_records_v2` as the source of truth.

See [Runtime modes](runtime.md), [Security model](security-model.md), and
[Runtime memory](runtime-memory-fabric.md).
