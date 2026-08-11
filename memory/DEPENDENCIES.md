# DEPENDENCIES.md — Durable Dependency Ledger

## Purpose

Record why important dependencies exist and when they need revalidation.

Do not duplicate the package manager lockfile.

## Record

```yaml
name: unknown
kind: runtime | build | test | ci | external_service
source: unknown
version_policy: repository_native
reason: unknown
owner: unknown
license: unknown
provenance_status: unknown
security_status: unknown
introduced_by_task: null
replacement_or_removal: unknown
last_reviewed: null
revalidation_triggers: []
```

## Which Dependencies Belong Here

Record dependencies when at least one is true:

- security-sensitive,
- operationally significant,
- difficult to replace,
- external service/vendor,
- unusual license,
- privileged build/CI integration,
- major architecture dependency.

Do not manually inventory every trivial transitive package.
