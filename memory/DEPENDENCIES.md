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

## Runtime V1 Dependencies

```yaml
name: modernc.org/sqlite
kind: runtime
source: https://gitlab.com/cznic/sqlite
version_policy: exact_direct_version_in_go.mod
version: v1.56.0
reason: canonical transactional local state through database/sql without requiring CGO
owner: runtime
license: BSD-3-Clause; bundled upstream SQLite code is public domain
provenance_status: canonical GitLab upstream and Go module checksum database
security_status: reviewed for Runtime V1 introduction
introduced_by_task: local-runtime-v1
replacement_or_removal: replace behind database/sql only when measured multi-host requirements outgrow SQLite
last_reviewed: 2026-08-11
revalidation_triggers:
  - major version or module path change
  - upstream governance or maintainer change
  - license change
  - SQLite or driver security advisory
  - Go baseline incompatibility
```

```yaml
name: go.yaml.in/yaml/v3
kind: runtime
source: https://github.com/yaml/go-yaml
version_policy: exact_direct_version_in_go.mod
version: v3.0.5
reason: strict parsing of the repository-owned CAPABILITIES.yaml security policy
owner: runtime-policy
license: MIT and Apache-2.0
provenance_status: canonical Go YAML upstream and Go module checksum database
security_status: reviewed for Runtime V1 introduction
introduced_by_task: local-runtime-v1
replacement_or_removal: removable only if CAPABILITIES moves to a standard-library-readable canonical format
last_reviewed: 2026-08-11
revalidation_triggers:
  - major version or module path change
  - upstream governance or maintainer change
  - license change
  - parser security advisory
  - CAPABILITIES schema change
```
