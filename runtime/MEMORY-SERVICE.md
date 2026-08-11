# Canonical Memory / Control-Plane Service

## Mission

Provide transactional shared state for multiple agents while preserving the
file-first semantics already defined by the pack.

## Canonical Entities

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
- dependency_records
- trace_links

See `runtime/SCHEMA.yaml`.

## API Semantics

Logical operations:

```text
status
task_list
task_get
task_claim
task_release
task_transition
agent_register
agent_heartbeat
decision_record
finding_record
finding_transition
handoff_record
checkpoint_create
approval_request
approval_consume
artifact_register
memory_recall
memory_remember
audit_query
```

## Atomic Claims

Task claim must be transactional.

Pseudo-rule:

```text
if task.status != ready:
    reject

if active lease exists:
    reject

create lease
set task.claimed_by
set task.status = claimed
commit
```

## Optimistic Concurrency

Mutable records should carry a revision/version.

Update shape:

```text
expected_revision
→ update
→ new_revision
```

Stale update returns conflict, not silent last-write-wins.

## Derived Retrieval

Semantic/graph/history indexes consume canonical records and repository/session
data.

If index rebuild is required:

```text
canonical state remains authoritative
```

## File Synchronization

When using this runtime with the Markdown pack:

- runtime DB is canonical for live coordination,
- Markdown snapshots are human-readable exports/checkpoints,
- import/export must preserve IDs and provenance,
- do not run two independent canonical writers without reconciliation policy.
