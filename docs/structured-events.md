# Structured event stream operations

The durable SQLite event stream is the source of truth. Live subscribers are a lossy convenience layer: durable append happens first, and clients recover missed items with `Since(last_sequence, limit)`.

Sequence and idempotency correctness are database-backed across Store instances. Exact retries converge to the canonical event; mismatched reuse of an idempotency key conflicts. Slow subscribers do not block publication. A subscriber-drop audit hook is bounded so a non-cooperative hook cannot stall the publisher indefinitely; durable history remains recoverable even when live delivery is lost.

A09 metrics expose only closed operation/outcome counters and aggregate duration. Event IDs, subjects, task/run/resource/evidence IDs, payload values, provider data, and secrets are never metric labels. Metrics are non-authoritative and cannot affect validation, authorization, sequence assignment, persistence, or subscriber resume.

Benchmark baselines cover bounded payload validation, in-process fanout, and authorized producer processing. They are local regression baselines rather than contractual SLOs; release confidence comes from the full persistence, race, fuzz, restart, backpressure, and secret-containment suites.
