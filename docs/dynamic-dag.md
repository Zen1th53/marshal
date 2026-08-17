# Dynamic DAG operations

MARSHAL's dynamic DAG is canonical in SQLite. Provider prose and metric state are never inputs to dependency satisfaction, authorization, or lifecycle transitions.

Operators should treat `ready` as a computed view of durable parent states. Cycle rejection is enforced again at the database write boundary, so a service pre-check is not the sole correctness control. Multi-store contention is retried with bounded, context-aware SQLite retry logic; exhausted contention remains a failed mutation rather than an assumed success.

The A09 metrics projection uses only closed operation/outcome vocabularies and aggregate durations. Task IDs, request IDs, subjects, provider names, evidence payloads, and secrets are not metric labels. Metrics are process-local diagnostics and cannot grant authority or rewrite readiness.

Benchmark baselines cover readiness, deterministic topological ordering, and reverse-edge lookup on representative 100-node graphs. They are regression signals, not release SLO guarantees; release decisions continue to require functional, concurrency, security, and integrity gates.

## Stable errors and events

The operator/API boundary uses stable machine-readable reason codes. Public messages remain bounded and never contain backend/provider error bytes:

- `DAG_CYCLE` — the candidate edge would make the durable graph cyclic.
- `DAG_NODE_NOT_FOUND` — a referenced task node is not present.
- `DAG_DUPLICATE_EDGE` — the same endpoint pair already exists with incompatible semantics.
- `DAG_INVALID_CONDITION` — the requested predecessor condition is outside the closed condition vocabulary.

Canonical durable event names are `dag.node.added`, `dag.edge.added`, `dag.node.ready`, and `dag.node.blocked`. Cycle rejection is represented by `dag.cycle.rejected`. Events carry IDs/digests and bounded result/reason fields; they are evidence/observability projections and never become authorization inputs.

On retry after an uncertain response, the canonical SQLite state and deterministic event idempotency key are reconciled. A restart never reconstructs authority from an old event: mutation authority and freshness are evaluated again at the service boundary.
