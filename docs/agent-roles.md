# Agent roles and authority boundaries

MARSHAL evaluates role authority and concrete capability separately. A role
such as `developer` may declare `source.write`, but a matching scoped
capability grant is still required before a privileged adapter is invoked.
Unknown roles, authorities, foreign subjects, cancelled requests and missing
capability grants fail closed with typed authorization reasons.

## Operator behavior

An allowed decision contains the principal, authority, resource and (when a
capability check is used) the matched grant identifier. A denied decision
contains a stable reason such as `AUTHZ_DENIED`, `AUTHZ_UNKNOWN_AUTHORITY` or
`AUTHZ_ROLE_INVALID`; raw prompts, provider text and secret material are not
part of the decision or audit projection.

Role bindings are persisted in SQLite with immutable identity and policy-digest
fields. Identical retries converge on one row, revocation uses a durable
compare-and-set, and competing revocations produce one winner and one typed
conflict. Reopening the database reloads the binding; cancellation before a
write leaves no binding.

## Events and diagnostics

`authz.authority.allowed` and `authz.authority.denied` are bounded audit
projections. Their resource reference is hashed and they do not grant
authority. `CanObserved` exposes aggregate authority success/denial/invalid
counts and duration through the existing process-local metrics recorder. The
metric operation is the closed value `authority`; identifiers, resources,
provider labels and error text are not metric dimensions. Metrics are
diagnostic only and cannot change a decision or authorize a transition.

The runtime role-aware process adapter is installed only when an authenticated
principal, authority and capability broker are supplied in runtime options.
An unconfigured runtime retains its existing provider behavior; it must not be
described as role-enforced until those options are configured.
