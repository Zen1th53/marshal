# Structured events

MARSHAL structured events are provider-neutral records persisted in the
canonical SQLite store before live delivery. Producers use the runtime event
API; consumers reconnect with `EventsSince` and a sequence checkpoint.

Each event carries stable identifiers for its subject, task, run, resource and
evidence where available. `Data` is bounded and rejects sensitive field names;
raw prompts, tool output and credentials are not an event transport.

Authorization is evaluated by the owning policy boundary through
`AppendAuthorized`. Missing, denied or stale decisions fail closed. A failed
live delivery does not erase durable history; consumers recover from the last
sequence. Retries with the same idempotency key return the existing durable
event when the payload matches and reject conflicting payloads.

Operator-facing metrics expose appended, denied and invalid counts, total append
duration, and the last stable failure code. Metrics are projections only and
never authorize an operation.
