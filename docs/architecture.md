# MARSHAL architecture

MARSHAL separates durable engineering authority from the provider process that
performs work. This page describes current `main` at SQLite schema v85, which is
ahead of the v1.5.0 tag.

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

## Governed lifecycle

Single-task execution above is one path through the runtime. `main` also
implements a six-stage governed lifecycle, where each stage is a durable
versioned record bound to an exact repository state.

```text
Request
   |
   v
Goal            internal/goalintake   intent, hard constraints, risk tier
   |
   v
Plan            internal/plan         task DAG, team, verification policy
   |
   v
Execution       internal/execution    sandboxed runs, checkpoints, evidence
   |
   v
Verification    internal/verification independent verdict + attestation
   |
   v
Learning        internal/learning     evidence-gated durable memory
   |
   v
Optimization    internal/optimization counterfactuals, bounded canaries
   |
   +--> proposals that touch policy re-enter as a new Goal

Cross-cutting: policy, capability, sandbox, network, approvals,
budget, checkpoints, provenance.
```

Each stage derives its binding from canonical state rather than accepting it
from a caller. Verification reads the stored run; learning reads the stored
completion attestation; optimization reads the stored memory commit. A caller
cannot supply a digest, a tree hash or an outcome and have it believed.

Schema v85 carries the durable stores for these stages, including append-only,
digest-protected records for completion attestations, memory commits and
optimization cycles. Mutable rows use compare-and-swap on their version, so a
stale writer is refused rather than overwriting newer state.

Implementation and qualification records: [process-06](process-06/),
[process-07](process-07/), [process-08](process-08/).
