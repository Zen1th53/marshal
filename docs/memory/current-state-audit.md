# MARSHAL memory authority map

**Current release:** v1.0.1

**Current schema:** v72

**Canonical store:** `memory_records_v2`

This document began as the T77 baseline audit. The original migration gaps
have since been resolved; the table classifications below describe the current
schema and prevent new memory-like tables from becoming an undocumented source
of truth.

## Canonical and compatibility tables

| Table | Current classification |
|---|---|
| `memory_records_v2` | Canonical durable memory source of truth |
| `memory_records` | Preserved legacy compatibility table; not used for canonical v2 recall |
| `persistent_agent_memory` | Preserved legacy compatibility table; no independent canonical runtime path |
| `decision_records` | Preserved legacy decision table; not a second general-memory authority |
| `failure_memory_records` | Preserved legacy failure table; not a second general-memory authority |
| `memory_outbox` | Transactional mutation log for derived index consumers |
| `memory_retrieval_receipts` | Caller-bound retrieval audit receipts and evidence links |
| `task_memory_event_heads` | Per-task durable notification cursor head |
| `task_memory_events` | Bounded task-memory change notifications; never stores memory bodies |

`task_memory_events` consumers reload authorized records from
`memory_records_v2`. The event window is bounded; an expired cursor requires a
canonical reload. Private/operator-scoped writes do not expose record bodies
through the cursor.

## Derived decision tables

The following tables are subsystem decisions, not general memory stores:

- `decisions`
- `gate_decisions`
- `egress_decisions`
- `context_budget_decisions`
- `model_router_decisions`
- `constitutional_decisions` (Process 00 gate verdicts on material actions)

They must not be merged into recall as if they were user/project memory.

`constitutional_decisions` records what was decided about an action — the
domain, the outcome, the reason code and the approval binding digest — and
never what the project knows. Its sibling tables `session_constitutions` and
`constitutional_violations` are likewise control state rather than memory:
they bind a session to a constitution version and record breaches. Memory that
Process 00 governs still lives in `memory_records_v2`; the constitutional
tables only decide whether a candidate may be written there.

## Canonical lifecycle

`app.MemoryService` is the product-facing path used by Runtime, CLI, MCP, A2A,
and supported live Web memory handlers. It applies store-level private-scope
filtering, principal/task/project ACLs, lifecycle and expiry checks, repository
freshness, bounded ranking, and retrieval receipts before rendering provider
context.

Run completion captures deterministic evidence-linked candidate memory. It
does not persist raw hidden reasoning or provider transcripts. Promotion,
supersession, tombstoning, conflict resolution, and consolidation remain
governed mutations.

Lexical, vector, graph, and cache structures are derived projections. They can
improve retrieval but do not replace SQLite canonical truth.

See [Runtime memory fabric](../runtime-memory-fabric.md) and the schema
inventory enforcement in `internal/store/memory_audit_t77_test.go`.
