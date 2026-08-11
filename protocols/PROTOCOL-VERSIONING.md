# PROTOCOL-VERSIONING.md

## Protocols Are Versioned Dependencies

Track:
- protocol name,
- negotiated version,
- extensions,
- endpoint identity,
- compatibility decision,
- verification date.

## Breaking Mismatch

Never "best effort parse" a security-sensitive incompatible protocol version.

Return:
```text
incompatible
upgrade_required
downgrade_required
probe_required
```

## Extensions

Each extension has:
- stable ID/URI,
- version,
- lifecycle status,
- required security review.

Use `schemas/protocol-extension.schema.json`.
