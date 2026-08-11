# SUPPLY-CHAIN.md — Dependency and Build Supply-Chain Protocol

## Mission

Make dependency and build trust explicit.

## Dependency Addition

Before adding a meaningful dependency:

```text
need
→ existing capability check
→ upstream/provenance
→ license
→ security
→ maintenance
→ transitive/build scripts
→ local fit
→ removal path
→ tests
```

Use `memory/DEPENDENCIES.md` for durable significant dependencies.

## Lockfiles

Unexpected lockfile churn is evidence of scope expansion or environment drift until explained.

## CI Actions / Images

Review:
- source owner,
- pinning policy,
- mutable tags,
- permissions,
- secret access,
- build scripts.

## SBOM

Generate/retain SBOM when project/release risk justifies it.

SBOM presence is inventory evidence, not proof of safety.

## Binary Dependencies

Prefer verifiable provenance and checksums/signatures according to project policy.

## Revalidation

Re-review when:
- major version changes,
- maintainer/source changes,
- license changes,
- security advisory appears,
- dependency becomes abandoned,
- privilege/network scope grows.
