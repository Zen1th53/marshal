# Runtime Multi-Tenancy

Shared runtime deployments must include tenant/project scope in authorization and
storage keys.

## Required Scope

```text
tenant_id
project_id
agent/session
task/resource
```

## Storage

Unique resource IDs are not authorization.

Every query scopes tenant/project before returning data.

## Retrieval

Semantic/graph/history adapters receive mandatory tenant/project filters.

## Events

Event subscribers must not receive events for unauthorized tenants.

## Secrets / Artifacts

Secret leases and artifact access are tenant/project bound.
