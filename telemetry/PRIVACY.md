# Telemetry Privacy

## Default

Metadata over content.

Prefer:
```text
task ID, duration, status, model/adapter, tool name
```

over:
```text
full prompt, full response, full source file
```

## Sensitive Data

Apply `memory/DATA-POLICY.md`.

SECRET:
- never telemetry.

CONFIDENTIAL:
- default deny payload capture,
- metadata-only unless explicitly authorized.

## Retention

Telemetry retention is independent from memory retention.

Do not retain traces forever by default.
