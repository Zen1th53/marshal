# Dynamic task DAG

The runtime exposes the provider-neutral DAG query surface through
`internal/app.Runtime.DAG`. Nodes and edges are persisted in the canonical
SQLite store; readiness is derived from predecessor state and topological order
is deterministic (priority descending, then task ID).

Mutations remain behind the DAG authorization boundary. Successful node/edge
mutations can record bounded `dag.*` events through the canonical event store;
event history is audit data and never grants authority. SQLite compare-and-swap
protects lifecycle transitions across runtime processes, with bounded retries
for transient database-lock contention.

Operational metrics expose bounded success, denied, invalid, and total-duration
counters. They are observational only and do not affect scheduling or policy
decisions.
