# Pack Distribution Protocol

## Goals

- reproducible pack identity,
- integrity verification,
- safe project-local overrides,
- reviewable upgrades,
- rollback.

## Pack Identity

Bind distribution to:

```text
pack version
schema version
manifest digest
individual file digests
```

## Project Installation

Recommended layout:

```text
repo/
├── AGENTS.md or native adapter file
└── agents/
    └── <this pack>
```

Native root instruction file stays small.

## Local Overrides

Do not edit vendor/core pack files if a project-specific override layer can express
the requirement.

Keep project rules in repository-native governance files.

## Upgrade

```text
download/new pack
→ verify manifest
→ diff versions
→ inspect breaking changes
→ migrate state if required
→ run conformance
→ switch
→ preserve rollback point
```
