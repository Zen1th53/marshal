# DATA-POLICY.md — Data Classification and Retention Defaults

## Classes

### PUBLIC
Safe for public repository/docs.

### INTERNAL
Project/team data not intended for public release.

### CONFIDENTIAL
Sensitive business/security data requiring controlled access.

### SECRET
Credentials, private keys, bearer tokens, recovery material, production secrets.

## Default Handling

| Class | General Memory | Semantic Index | Logs | External Tools |
|---|---|---|---|---|
| PUBLIC | allowed | allowed | allowed | task-dependent |
| INTERNAL | allowed scoped | scoped | minimal | policy-dependent |
| CONFIDENTIAL | minimal/scoped | default deny | redacted/minimal | explicit permission |
| SECRET | forbidden | forbidden | forbidden | forbidden unless dedicated secret workflow |

## Retention & Lifecycles

Project policy owns exact durations.

Default principles:

```text
retain only while it has operational, legal, security, or engineering value
```

- **Working Memory**: Default TTL of 24 hours. Evaluated and staled if not promoted.
- **Commit-Bound Facts**: Automatically flagged as `stale` when referenced `head_commit` diverges.
- **Tombstones**: Explicit deletion marks the record as `tombstoned`, purges the sensitive payload `[PURGED]`, and emits a `memory.tombstoned` outbox event to purge downstream vector and search indexes.
- **Audit Invariant**: Minimal audit metadata is retained while prohibited deleted text is completely purged.

Do not retain raw transcripts merely because storage is cheap.
