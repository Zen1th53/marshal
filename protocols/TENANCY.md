# TENANCY.md — Shared-Service Isolation Protocol

## Rules

- Every shared-service request carries tenant/project context.
- Canonical store queries scope before returning records.
- Semantic/graph/history retrieval is tenant-filtered before ranking.
- Secret leases are tenant/project bound.
- Artifact URIs are not authorization.
- Telemetry includes tenant/project IDs but not secret/customer payloads by default.
- Cross-tenant operations require a distinct platform-admin permission and audit.
