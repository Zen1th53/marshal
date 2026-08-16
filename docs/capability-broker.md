# Capability Broker

The capability broker is the explicit authorization boundary for privileged
filesystem, process, Git, network, secret, MCP, and deployment actions. A
grant is bound to a subject, task, capability kind, canonical resource, action
set, issuer, and finite expiry. The durable store is authoritative for grant
state; process-local memory is not.

## Operator behavior

An authorized caller issues a scoped grant and then presents the same subject,
task, kind, canonical resource, and action to the broker. A matching active
grant returns `ALLOW` and its grant identifier. No matching, expired, revoked,
stale, or foreign grant returns a typed `DENY` reason. The adapter boundary
checks this decision before invoking a provider process runner.

Example success: a broker-issued `shell.exec` grant for `/workspace/build`
with action `execute` allows the matching task to reach the process adapter.
Example denial: the same task requesting `/workspace/other`, or an expired
grant, is denied and the process adapter is not called.

## Diagnostics and events

Capability authorization can attach the existing in-process
`evidence.MetricsRecorder` through `capability.NewObservedEngine`. It records
only the closed `capability` operation and bounded result/reason vocabularies,
plus aggregate monotonic duration. It does not persist identifiers, resources,
subjects, provider data, or errors, and it cannot affect authorization.

Audited engines emit bounded capability grant/authorization/revocation events.
Events carry correlation identifiers and hashed resource references rather
than raw resource text or secret material. Durable grant state is written
before downstream event reconciliation; retrying an idempotent request does
not create a second grant.

## Recovery and limitations

Grant idempotency is enforced by the durable idempotency key and unique
constraint, so independent store instances converge on one grant. Revocation
uses a durable compare-and-set and a revoked grant cannot be finalized by a
foreign actor. Cancellation before persistence leaves no grant.

The broker's metrics are process-local diagnostics, not a Prometheus endpoint
or authority cache. The active-grant state remains in SQLite and must be
reloaded after restart. Runtime process enforcement is installed when
`Options.CapabilityBroker` is supplied; an unconfigured runtime retains its
legacy provider behavior and must not be described as capability-enforced.
