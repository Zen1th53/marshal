# Structured event stream operations

The durable SQLite event stream is the source of truth. Live subscribers are a lossy convenience layer: durable append happens first, and clients recover missed items with `Since(last_sequence, limit)`.

Sequence and idempotency correctness are database-backed across Store instances. Exact retries converge to the canonical event; mismatched reuse of an idempotency key conflicts. Slow subscribers do not block publication. A subscriber-drop audit hook is bounded so a non-cooperative hook cannot stall the publisher indefinitely; durable history remains recoverable even when live delivery is lost.

A09 metrics expose only closed operation/outcome counters and aggregate duration. Event IDs, subjects, task/run/resource/evidence IDs, payload values, provider data, and secrets are never metric labels. Metrics are non-authoritative and cannot affect validation, authorization, sequence assignment, persistence, or subscriber resume.

Benchmark baselines cover bounded payload validation, in-process fanout, and authorized producer processing. They are local regression baselines rather than contractual SLOs; release confidence comes from the full persistence, race, fuzz, restart, backpressure, and secret-containment suites.

## Stable errors and events

The stream exposes stable machine-readable reason codes with bounded public messages:

- `EVENT_TYPE_INVALID` — event type is outside the registered vocabulary.
- `EVENT_SECRET_FIELD` — event data failed the secret/sensitive-data boundary.
- `EVENT_STORE_FAILED` — canonical persistence or a required post-commit audit operation failed.
- `EVENT_SEQUENCE_CONFLICT` — an idempotency identity was replayed with different semantic content.

Canonical self-observability events are `events.appended`, `events.subscriber.dropped`, and `events.schema.rejected`. They contain bounded IDs/digests and result/reason metadata, not raw provider prompts, tool output, tokens, or secret values.

A live-delivery failure does not erase a committed producer event. Callers recover by sequence with `Since`; exact idempotent retries converge to the same durable event. Authorization and freshness are evaluated from canonical runtime identity and never inferred from historical events.
