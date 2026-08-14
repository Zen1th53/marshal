# Local Runtime V1 Design

## Purpose

Implement the first executable MARSHAL runtime as a small local control plane:
a thin CLI, a local Unix-socket daemon, one canonical SQLite writer, enforced
policy and approvals, task-scoped Git worktrees, a real Codex adapter, durable
events, and digest-bound evidence.

This milestone proves the existing runtime contracts on one Linux host. It does
not implement a distributed runtime, remote A2A/MCP servers, a production
secrets broker, or adapters other than Codex.

## Runtime Boundary

The `marshal` CLI is a client. Protected state mutations go through a daemon
listening on `.marshal/runtime.sock`; the CLI does not silently fall back to
writing SQLite directly. The daemon is the single canonical writer while it is
running.

`marshal init` is the bootstrap exception. It verifies Git repository identity,
creates the project-local runtime directory, applies deterministic migrations,
and records the project before a daemon exists. It is idempotent and does not
create tasks or overwrite governance files.

The daemon exposes a versioned local HTTP/JSON API over a Unix socket. The
runtime directory has mode `0700` and the socket has mode `0600`. Every
response includes a request ID and either a typed result or a typed error. No
TCP listener or unauthenticated network surface is introduced.

## Canonical Data

SQLite schema version 1 contains the contract entities:

- projects
- agents
- sessions
- tasks
- task_dependencies
- leases
- decisions
- findings
- handoffs
- checkpoints
- approvals
- artifacts
- audit_events
- memory_records

Two execution records are also required: `worker_runs` binds process results to
tasks and sessions, and `verifications` stores commit-bound verification
status. `schema_migrations` records applied migrations.

Foreign keys are enabled. WAL and a bounded busy timeout support the
single-host concurrent read/write model. Mutable coordination records carry a
revision. Stale writes return conflict; there is no timestamp-based
last-write-wins behavior.

The runtime uses `modernc.org/sqlite`: it implements `database/sql`, is
actively maintained, requires no CGO toolchain, and supports the single-binary
distribution goal. The exact reviewed version is pinned in `go.mod` and
recorded in `memory/DEPENDENCIES.md`.

## Task Import and Reconciliation

`marshal task import tasks.json` accepts either one task object or an array of
task objects matching `schemas/task.schema.json`. Validation is strict:
unknown fields, malformed IDs, invalid states, duplicate dependency IDs, and
missing referenced tasks reject the entire import transaction.

Import is idempotent when an existing task has the same canonical content and
revision. The same task ID with divergent content returns conflict. The
`--dry-run` form performs the minimum read-only reconciliation path: it
reports additions, matches, and conflicts without mutation. Runtime SQLite is
canonical for live coordination; the imported JSON remains an external
snapshot, not a second writer.

## Identity and Sessions

Concrete agents are registered by the local operator:

```text
marshal agent register --name local-codex --role developer
```

The role is stored on the agent record and copied into an immutable session
binding. Privileged requests identify agent and session IDs; an arbitrary role
string in a worker request is never authoritative. The worker sandbox does not
mount the runtime socket, so a worker cannot register a stronger identity.

Claim and run commands require an agent ID when more than one eligible agent
exists. With exactly one eligible agent, the CLI may select it
deterministically. Sessions persist task, project, branch, worktree, start time,
heartbeat, and terminal status. Missing heartbeat is liveness evidence only
and never authorizes task theft.

## Atomic Claims and Scheduling

A claim uses one write transaction:

1. verify the task revision and `ready` status;
2. verify all hard dependencies are complete;
3. verify no active conflicting lease;
4. create the session-bound lease;
5. bind task ownership, transition to `claimed`, and increment revision;
6. append `TASK_CLAIMED` in the same transaction.

A partial unique index permits one active lease per task. Concurrent contenders
receive a conflict without silent retry. Lease expiry only marks work stale;
reclaim requires explicit inspection and is outside automatic Runtime V1
behavior.

The V1 scheduler calculates a deterministic ready list from status,
dependencies, active leases, and pre-gates. Dispatch is operator-requested;
there is no speculative background auto-dispatch.

## Policy and Approvals

The daemon strictly loads `CAPABILITIES.yaml`. If policy is unavailable or
invalid, health reports failure and privileged execution fails closed.

Policy inputs bind agent, session, stored role, task, risk, semantic operation,
target, environment, and approval. Decisions are `ALLOW`, `DENY`, or
`REQUIRE_APPROVAL`. The exposed execution interface is intentionally narrow;
there is no general shell-string API. Operations include:

- filesystem.read and filesystem.write
- shell.execute
- network.access
- git.commit, git.push, and git.history_rewrite
- secret.read and external.upload
- deploy and destructive_operation

History rewrite, production mutation, secret access, external upload, and
destructive operations deny by default or require approval according to the
capability policy. Alternate command spelling cannot bypass a semantic
decision because the runtime does not expose alternate raw execution paths.

Approval validation binds operation, scope, target, commit, expiry, and status.
Expired, consumed, revoked, wrong-scope, wrong-target, or wrong-commit records
fail. Successful privileged use consumes the approval in the same transaction
as the protected transition.

Finding transitions preserve role authority. A developer cannot close a
QA-owned or AppSec-owned finding.

## Worktrees and Sandbox

Every implementation run has one task branch and one Git worktree. Before
launch, the runtime verifies repository root, base and current commits, branch,
worktree path, dirty state, and lease owner. Two active tasks cannot share one
writable worktree.

Unexpected dirty work is preserved. The runtime never resets it, deletes its
worktree, or overwrites its branch. A dirty or contradictory worktree blocks
launch and records evidence.

Linux Runtime V1 implements a bubblewrap backend. It exposes the task worktree
as writable, required system paths as read-only, isolated temporary and home
directories, and process namespaces. The runtime probes backend usability, not
only binary presence.

When task policy denies network, bwrap uses a network namespace. Codex requires
network for its model API, so a network-denied Codex task is blocked before
launch; it never degrades to unrestricted host execution. If bwrap is
unavailable, isolation is reported as `process_only`. R2 and R3 execution is
blocked instead of silently degrading. R0/R1 fallback is allowed only when
policy permits it and is reported explicitly.

## Worker and Codex Adapter

The worker manager implements REGISTER, ASSIGN, PREPARE, RUN, HEARTBEAT,
CHECKPOINT, VERIFY, RELEASE, and EXIT as persisted lifecycle transitions.
Processes run in their own process group with a wall timeout and cancellation.
Termination first signals the process group and then kills it after a bounded
grace period.

The first real adapter implements the repository adapter contract around the
installed native surface:

```text
codex exec --json -C <worktree> -s workspace-write -a never --ephemeral
```

It never uses dangerous sandbox or approval bypass flags. The adapter probes
binary and version, builds a structured task prompt, records sandbox and
approval modes, captures JSONL output, extracts a session ID when present,
stores stdout/stderr as artifacts, and returns a normalized execution
envelope.

Codex authentication is mounted as narrowly and read-only as the installed CLI
permits; the worker does not inherit the general user home. Runtime V1 does not
claim that this is a production secret broker or perfect hostile-worker
credential isolation. Real Codex execution is therefore limited to R0/R1.

The adapter interface is justified by the real Codex implementation and the
existing `adapters/CONTRACT.md`. A deterministic fake adapter is test-only and
does not replace the real adapter.

## Run State Machine

`marshal run TASK-001 --adapter codex` performs:

1. authenticate the local request and create/bind a session;
2. load the task and evaluate scheduler readiness;
3. enforce policy and any required approval;
4. claim the task and lease atomically;
5. create or verify the task worktree;
6. verify sandbox capability;
7. launch and monitor Codex;
8. capture output, events, artifacts, exit status, and repository state;
9. invalidate old verification if HEAD changed;
10. transition only to a reviewable or blocked state;
11. release worker resources while preserving evidence.

Exit zero plus a new commit and clean worktree transitions the task to
`review`. Exit zero without a reviewable commit, or with a dirty worktree,
transitions to `blocked` and preserves the worktree. Nonzero exit, timeout, or
crash marks the session failed/stale and never marks the task complete. A
developer worker cannot issue QA/AppSec approval or merge itself.

## Events, Artifacts, and Verification

State transitions and their durable events commit together. `audit_events`
acts as the V1 outbox. Stable event IDs make duplicate insertion idempotent
only when the payload matches; reuse with different payload returns conflict.
No global delivery ordering is promised.

The event set includes TASK_READY, TASK_CLAIMED, TASK_RELEASED, TASK_BLOCKED,
HEAD_CHANGED, VERIFICATION_INVALIDATED, WORKER_STARTED, WORKER_EXITED,
APPROVAL_REQUIRED, POLICY_DENIED, and ARTIFACT_REGISTERED.

Artifact bytes are stored under
`.marshal/artifacts/sha256/<digest>`. Registration computes SHA-256 and rejects
a claimed digest that does not match. Evidence binds task, agent/session,
adapter/version, base and resulting commits, timing, exit status, exact
verification command, output references, and artifact digest.

`marshal verify` checks runtime/store/policy/worktree/artifact integrity and
records exactly those checks at the current HEAD. It does not imply project
tests ran. `marshal verify -- <argv...>` executes exact argv without a shell,
after policy evaluation, and records its actual exit status and output digest.

If HEAD changes from A to B, every valid verification for A becomes invalid in
the same transaction that appends HEAD_CHANGED and
VERIFICATION_INVALIDATED.

## CLI Contract

Runtime V1 provides:

```text
marshal init
marshal doctor
marshal status
marshal agent register
marshal agents
marshal tasks
marshal task import
marshal task show
marshal task claim
marshal task release
marshal run
marshal events
marshal artifacts
marshal verify
marshal daemon
```

Read-oriented commands support global `--json`. Human output is concise; JSON
is the automation contract. Exit codes follow `runtime/MARSHAL-CLI.md`: success,
generic failure, usage, policy denied, approval required, conflict, unavailable
dependency, and verification failure.

## Error Handling

Errors are explicit and typed. Database corruption, missing policy, invalid
migrations, repository mismatch, and unsafe worktree state are failures.
Missing Codex is degraded health when no Codex run is requested and unavailable
when it is. Missing bwrap is degraded for allowed R0/R1 process-only work and a
hard block for R2/R3.

No failure path deletes a dirty worktree, fabricates runtime state, promotes a
task to complete, consumes an unrelated approval, or reports a stronger
isolation level than was actually established.

## TDD and Verification Strategy

Every behavioral slice follows Red, Green, Refactor and ends in an independently
buildable commit.

Tests include:

- deterministic migration and idempotent init tests;
- real SQLite concurrent claims where exactly one contender succeeds;
- session, heartbeat, stale revision, lease, and dependency tests;
- approval expiry/scope/target/commit/status tests;
- QA/AppSec finding ownership tests;
- policy denial and secret-memory rejection tests;
- real temporary Git repository worktree and dirty-preservation tests;
- HEAD-change verification invalidation tests;
- worker crash/timeout/process-group tests;
- bwrap network-denial and fallback tests;
- artifact digest mismatch tests;
- daemon Unix-socket and CLI JSON integration tests;
- deterministic fake-adapter end-to-end tests;
- opt-in real installed/authenticated Codex integration.

The runtime tests are connected to the relevant behavioral conformance
scenarios: CONF-003, CONF-004, CONF-005, CONF-006, CONF-008, CONF-009,
CONF-010, CONF-018, CONF-019, CONF-023, CONF-024, CONF-025, and CONF-026.
Static fixtures alone are not counted as behavioral PASS.

Final verification runs all existing Python/conformance checks plus
`go test ./...`, `go vet ./...`, and `go test -race ./...`. A real Codex
call is reported separately because it requires installed authenticated
external service access.

## Version and Scope

The pack remains version 6.0.0 and the runtime specification remains 1.0.0.
The first executable implementation version is 0.1.0. Documentation will state
narrowly that Local Runtime Milestone 1 is implemented and will continue to
identify distributed, multi-host, full MCP/A2A, production secret-broker, and
additional adapter work as unimplemented.
