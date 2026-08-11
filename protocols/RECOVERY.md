# RECOVERY.md — Control-Plane Backup, Restore, and Disaster Recovery

## Mission

Recover coordination/memory state without confusing it with source-code truth.

## What to Protect

Depending on risk:
- memory canonical state,
- task graph,
- decisions/findings,
- approvals,
- artifact registry,
- traceability,
- pack version/config,
- audit records.

## Source Code

Git/repository recovery follows repository policy.

Memory restore must not silently reset source code.

## Backup

File-first systems may use:
- Git commits,
- snapshots,
- protected archives.

Service-backed systems may use:
- DB backups,
- transaction logs,
- replicated storage.

## Restore

```text
identify corruption/loss
→ freeze conflicting writers
→ select known-good backup
→ restore to isolated location
→ validate schema/version
→ compare with repository current state
→ mark stale records
→ resume only after reconciliation
```

## Restore Drill

A backup that has never been restored is unproven.

High-value deployments should periodically verify restore procedures.

## Corruption

If canonical state is corrupt and no verified restore exists:

```text
fail loudly
→ reconstruct from repository/CI/approved artifacts
→ mark uncertainty
```

Do not fabricate missing findings/approvals.

## Semantic Indexes

Semantic/vector/graph indexes should be rebuildable from canonical records when possible.

Treat them as derived state.

## Disaster Recovery Output

Record:
- incident,
- recovered source,
- pack/schema version,
- records lost/uncertain,
- repository reconciliation,
- verification performed.
