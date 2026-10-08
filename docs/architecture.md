# MARSHAL architecture

MARSHAL separates durable engineering authority from the provider process that
performs work. This page describes current `main` at SQLite schema v91, after
the v0.0.7 release (targeting v0.0.8).

```text
CLI / terminal TUI / Unix socket / MCP / A2A
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
points into the same runtime. Community permits HTTP encoding over the project-local Unix socket. It exposes
no Web UI, TCP/Web control-plane routes, or remote listener; those capabilities
are Enterprise-only. Socket reads require kernel peer credentials matching the
state-directory owner. A matching UID cannot distinguish an operator from an
agent running under the same account, so the socket carries only the worker
protocol (agents, tasks, verify, reconcile); operator commands are never routed
there.

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

Bubblewrap provides the strong Linux filesystem/process boundary. Governed
workers always use `--unshare-net`. A runtime-owned per-run Unix proxy, bridged
to sandbox loopback by trusted socat, enforces exact host/port grants. Only the
provider API is allowed by default; `/egress` accepts operator grants and
revocations. Decisions are durable, socket-incarnation-scoped events in the
shared project store, read by the owning proxy before dialing. The TUI can
therefore grant a daemon-owned run without exposing operator commands on the
worker socket. Status validates live proxy listeners from both runtimes;
revocation polling closes existing remote connections. Missing proxy/bridge/isolation refuses network work. Native
sessions opened directly are out of scope.

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

Schema v91 carries the durable stores for these stages, including append-only,
digest-protected records for completion attestations, memory commits and
optimization cycles. Mutable rows use compare-and-swap on their version, so a
stale writer is refused rather than overwriting newer state.

Implementation and qualification records: [process-06](process-06/),
[process-07](process-07/), [process-08](process-08/).

## Authenticated local application commands

The trusted in-process workspace obtains an immutable local principal from the
owner of the mode-`0700` state directory. A private context key binds the local
control session to its Runtime; command text supplies neither identity nor role.
The server composes its owner role authority with a durable concrete capability:
`fs.write`, scoped to the canonical database path, project, and `goal.revise`
action. Entitlement and executable discovery do not participate in this check.
Revoked grants are not recreated when a workspace or runtime restarts.

Goal revision is the first command boundary. Its envelope names the exact
project, session, goal, expected revision and idempotency key. The canonical
revision service reconstructs the original request and hard constraints and
resets confirmation to `PENDING`. Schema v91 includes `command_results` and
`command_audit`; the goal revision, receipt and audit insert commit together.
Immutable receipts bind the actor and hashed key to the envelope/payload digest
and resulting canonical revision. Replay returns that revision, even after the
active goal advances. Audit records contain identifiers and outcomes, not prose
or credentials. Insert-only triggers protect both new tables.

Worker subprocesses receive serialized task envelopes, not Go contexts or local
control handles. Agent registration, MCP, A2A and socket handlers cannot obtain
this workspace handle. This is an application entry-path boundary, not proof of
human presence or a sandbox against unrestricted same-UID host processes:
process-only workers retain host-account privileges. Operator socket mutations
remain disabled, and no new slash command or navigation release is enabled.

Migration 89 added task control intents and immutable task command snapshots.
Migration 90 adds the reasoning effort to the execution model preference, bound
to the same revision as the model it was validated against.
Operator task mutations use `task.create`, `task.assign` and `task.control`;
assignment shares the worker claim transaction's lease and dependency checks.
Pause settlement follows supervisor acknowledgement, and resume rechecks the
bound Goal and plan through the canonical handoff validation.
