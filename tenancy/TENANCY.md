# Tenancy Model

## Namespace

Canonical key:

```text
tenant_id
+ project_id
+ resource_type
+ resource_id
```

Applies to:
- tasks,
- memory,
- findings,
- approvals,
- artifacts,
- traces,
- secret leases,
- events.

## Isolation

A request must not access another tenant merely because an ID is guessable.

Authorization checks tenant/project before resource lookup result is disclosed.

## Shared Services

Semantic/vector indexes must retain tenant/project filter metadata.

Cross-tenant similarity search is denied by default.

## Admin

Platform admin capability is separate from project engineering roles.

Developer/Architect/QA/AppSec roles are not tenant administrators.
