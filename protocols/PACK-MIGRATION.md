# PACK-MIGRATION.md — Agent Pack Version and Schema Migration Protocol

## Mission

Allow the reusable agent system to evolve without silently changing authority or corrupting shared state.

## Before Upgrade

Read:
- `PACK-VERSION.yaml`,
- `CHANGELOG.md`,
- current repository overrides,
- memory schema/version.

## Breaking Changes

Treat these as breaking unless explicitly migrated:

- role authority,
- task state semantics,
- finding ownership,
- memory record identity,
- precedence/trust model,
- approval semantics.

## Migration

```text
snapshot current pack/memory
→ identify schema/policy delta
→ migrate copies
→ validate references/state
→ switch
→ preserve rollback point
```

## Repository Overrides

Do not overwrite project-specific `AGENTS.md`, policies, or custom role extensions blindly.

## Deprecation

Prefer:
```text
introduce new
→ support migration window
→ warn
→ remove later
```

for externally consumed schemas/interfaces.

## Verification

After upgrade verify:
- internal refs,
- manifest refs,
- state schema,
- task graph,
- active decisions/findings,
- approval semantics,
- role authority.
