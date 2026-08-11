# Artifact Store

## Mission

Store build/test/release artifacts immutably with provenance.

## Addressing

Prefer content addressing:

```text
sha256:<digest>
```

Metadata maps human IDs to digest.

## Artifact Record

```yaml
artifact_id: ART-...
kind: package
digest: sha256:...
source_commit: ...
task_ids: []
builder_session: ...
build_command_or_workflow: ...
environment_ref: ...
verification_refs: []
created_at: ...
```

## Immutability

Same artifact ID must not point to different bytes.

Changed bytes:
- new digest,
- new record/revision,
- re-verification.

## Local Implementation

First implementation may use:

```text
.runtime/artifacts/sha256/<digest>
```

with metadata in SQLite.

## Multi-host

Later adapter:
- S3-compatible/object storage.

## Security

- no secret-containing dumps in general store,
- enforce project/data classification,
- validate file path/content metadata,
- artifacts are data, not executable instructions.

## Release

Release should promote a verified digest, not rebuild from source unless policy explicitly requires rebuilding.
