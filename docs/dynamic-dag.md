# Dynamic DAG operations

MARSHAL's dynamic DAG is canonical in SQLite. Provider prose and metric state are never inputs to dependency satisfaction, authorization, or lifecycle transitions.

Operators should treat `ready` as a computed view of durable parent states. Cycle rejection is enforced again at the database write boundary, so a service pre-check is not the sole correctness control. Multi-store contention is retried with bounded, context-aware SQLite retry logic; exhausted contention remains a failed mutation rather than an assumed success.

The A09 metrics projection uses only closed operation/outcome vocabularies and aggregate durations. Task IDs, request IDs, subjects, provider names, evidence payloads, and secrets are not metric labels. Metrics are process-local diagnostics and cannot grant authority or rewrite readiness.

Benchmark baselines cover readiness, deterministic topological ordering, and reverse-edge lookup on representative 100-node graphs. They are regression signals, not release SLO guarantees; release decisions continue to require functional, concurrency, security, and integrity gates.
