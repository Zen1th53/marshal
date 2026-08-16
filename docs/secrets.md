# Secret Broker

T21 provides scoped secret use through `internal/secrets`. A caller submits a
reference (`provider`, `name`, `version`) and a task/subject/purpose-bound lease;
the resolved bytes are available only inside `WithSecret` and are zeroed by the
broker after the callback returns.

The production composition path is:

```text
app.Runtime.WithSecret
  → secrets.Engine
  → capability.Broker (secret.use/read)
  → provider.Resolve
  → callback
```

The default local provider is `EnvironmentProvider` (`provider: env`). It reads
only the requested environment variable; it does not enumerate the environment,
persist values, or print them. External keyring, Vault, and KMS providers must
implement the provider interface and remain behind the same capability check.

Successful use requires an active, unexpired lease and a matching capability.
Denied, expired, revoked, malformed, missing, or unavailable-provider requests
fail closed with stable `SECRET_*` reason codes. There is intentionally no CLI
command that prints or accepts raw secret values.

The SQLite `secret_leases` record stores only the reference and bounded metadata.
Schema v21 adds a durable access owner and claim timestamp. A live claim cannot
be stolen; a claim older than one minute can be reclaimed, and an old owner
cannot finalize it. Cancellation releases the claim. Terminal retry reads the
durable `used` state and does not resolve the provider again.

Lifecycle events are metadata-only:

- `secret.lease.requested`
- `secret.lease.issued`
- `secret.access.used`
- `secret.lease.revoked`
- `secret.access.denied`

Events contain subject/task/reference digest and bounded reason metadata, never
secret bytes. Metrics are an in-process observational projection only; they do
not authorize access or change a decision. The `secret` metric operation tracks
success, denial/error/cancellation, duration, active claims, and a closed reason
vocabulary.

Example success is an authorized `EnvironmentProvider` lookup inside a callback.
Example failure is the same reference with no `secret.use/read` capability, which
returns `SECRET_DENIED` before provider resolution. Use temporary local state in
tests and never use real credentials in fixtures.
