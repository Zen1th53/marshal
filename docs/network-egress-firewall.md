# Network egress enforcement

MARSHAL evaluates outbound requests with the provider-neutral `internal/netpolicy`
engine. Rules are normalized before matching, unknown protocols and invalid
ports fail closed, and the default decision is deny. A hostname rule does not
authorize a resolved IP unless an explicit IP rule matches it.

Runtime integration is deny-by-default. A task must explicitly request network
access (`network_required: true` in the structured run request), and the
canonical capability policy must allow task-owned network access before the
runtime gives a sandbox network namespace. The evaluator itself never opens a
socket or performs DNS; resolution and OS enforcement remain outside database
transactions.

Durable egress decisions are stored by immutable decision ID and idempotency
key. Successful retries reconstruct the existing decision. Subject-bound
decisions emit `network.egress.requested` followed by
`network.egress.allowed` or `network.egress.denied`; event data contains bounded
identifiers and reason codes, not prompts or payloads.

The runtime uses bubblewrap when available. If required isolation cannot be
provided, execution fails closed; this implementation does not silently fall
back to unrestricted network access. Metrics are an in-process operational
projection only and cannot authorize an operation.

## Examples

An allowed structured request includes an authenticated task identity and an
explicit network requirement. An unrequired request remains in a network-denied
sandbox even when the provider label is trusted. A request for `evilgithub.com`
does not match `github.com`, and redirects must be evaluated as new requests by
the mediated client.

## Limitations

The current repository has no general mediated HTTP client or DNS resolver
owned by T22. Callers must use `netpolicy.Evaluator` before performing those
operations; raw provider/network integrations are not implicitly authorized by
this package.
