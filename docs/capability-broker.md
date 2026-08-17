# Capability Broker

The capability broker is the single decision path for privileged actions. A
request is bound to an authenticated subject and task, then matched against a
durable grant with a normalized resource, closed capability kind, explicit
actions, and an expiry time.

## Operator behavior

No matching grant returns a structured `CAP_DENIED` decision. Expired or
revoked grants return `CAP_EXPIRED` or `CAP_REVOKED`. Missing authority fails
closed before a grant or process mutation is attempted. The worker capability
gate must run before the process runner; it does not start a shell, provider,
network client, or runtime action on denial.

Example allowed request: an authorized administrator issues `fs.read` for
subject `agent-1`, task `task-1`, resource `/workspace`, action `read`.

Example denied request: the same subject asks for `fs.write` or a different
task; the broker returns a non-allowed decision and the worker gate performs
zero process calls.

## Persistence and recovery

Capability grants are stored in the canonical SQLite `capability_grants` table.
The current schema is v13. Grant state is explicit: `requested`, `issued`,
`active`, `revoked`, or `expired`. Exact duplicate grant retries are
idempotent; a different payload using an existing grant ID is an immutable
conflict. Revoke uses a conditional update, so competing stores have one
durable winner.

Audit events use bounded metadata and deterministic IDs. Metrics are an
in-process, non-authoritative projection: they cannot grant authority or
change a decision. The capability operation metric uses only closed reason
codes and does not label by subject, task, resource, grant ID, or secret data.

## Limitations

The worker gate is an explicit adapter and must be installed by each process
runner composition point. Bubblewrap and network isolation remain platform
dependent; a failed isolation probe must not be treated as capability grant.
