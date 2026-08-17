# Typed Agent Handoffs

T28 makes handoff state a versioned, provider-neutral record. It carries
bounded claims, evidence IDs, changed repository-relative files, risks,
unresolved work, and a context digest. It does not convey capability,
authority, tokens, or secret fields.

## Canonical ownership

| Concern | Canonical path |
|---|---|
| Contract and state machine | `internal/protocol` |
| Durable typed records | `internal/store/typed_handoffs.go` |
| Runtime composition | `internal/app/runtime.go` |
| A2A transport | `internal/a2a/server.go` |
| Evidence ownership | T06 `evidence_nodes` |
| Role/capability ownership | T04/T01 authorization inputs |

The historical `handoffs` table remains untouched compatibility history. It is
not read by, migrated into, or treated as authority by the typed service.
`typed_handoffs` is the only canonical table for this contract.

## Lifecycle and security boundary

`created → validated → accepted|rejected → consumed` is enforced with a
conditional database transition. Sender identity is bound to the authenticated
principal; the recipient independently needs its own `handoff.consume`
authority. Evidence IDs must belong to the same task before persistence.

The A2A entry point is `POST /a2a/handoffs`. It requires an authenticated
`a2a_agent` bearer principal and accepts a `protocol.Submission` JSON envelope.
It returns stable error codes such as `HANDOFF_SENDER_FORGED`,
`HANDOFF_EVIDENCE_INVALID`, and `HANDOFF_VERSION_UNSUPPORTED`; it never uses
free-form text as an authoritative completion signal.

## Limits and observability

The contract caps claims at 32, evidence IDs at 64, changed files at 256, and
risks/unresolved items at 64. Individual text fields are capped at 4096 bytes.
The in-process protocol metrics expose accepted count, bounded deny/invalid
reason counts, active accepted records, accumulated duration, and a stable last
failure code. Durable `handoff.created`, `handoff.accepted`,
`handoff.rejected`, and `handoff.consumed` events contain IDs/digests only.

## Recovery

The unique idempotency key serializes competing submissions across processes.
Exact retries return the original lifecycle record; a conflicting reuse is
denied. Events are appended only after a durable state commit and use their own
idempotency keys, so retry after an uncertain event delivery converges without
duplicating a semantic transition.
