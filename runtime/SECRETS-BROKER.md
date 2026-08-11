# Secrets Broker

## Mission

Provide the minimum secret required for the minimum duration to the minimum task.

## Rules

- General memory never stores secret values.
- Runtime audit logs never store secret values.
- Worker receives secret only when policy allows.
- Secret should be scoped by task/resource/environment.
- Revoke/expire after use or worker termination.

## Lease

```yaml
secret_lease_id: ...
secret_name: ...
agent_id: ...
session_id: ...
task_id: ...
environment: ...
expires_at: ...
scope: ...
```

The secret value is delivered through a protected channel, not stored in the lease record.

## Backends

Adapters may include:
- environment/local OS keyring,
- SOPS-managed files,
- Vault,
- 1Password,
- cloud IAM/secret managers.

The pack does not require one backend.

## Production Credentials

Default deny.

Require:
- capability,
- task need,
- environment authorization,
- approval where policy demands,
- short duration.

## Failure

Broker unavailable:
- secret-dependent operation blocks,
- runtime does not substitute stale cached plaintext.
