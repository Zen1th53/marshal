# OBSERVABILITY.md — Agent Run Observability and Audit Protocol

## Mission

Make team actions debuggable without turning internal reasoning into logs.

## Observe Decisions, Not Thoughts

Record:
- state transitions,
- tool/command outcomes needed for audit,
- approvals,
- task ownership,
- findings,
- artifact/release events.

Do not record private chain-of-thought.

## Correlation

Where practical correlate:

```text
task ID
branch/commit
agent/role
artifact ID
finding ID
approval ID
```

## Failure

A failed tool call may be operationally relevant when it changes confidence or blocks work.

Do not log every harmless command.

## Retention

Follow `memory/DATA-POLICY.md`.

## Audit Integrity

For higher-risk environments, move audit records to append-only or server-side storage when file/Git history is insufficient.
