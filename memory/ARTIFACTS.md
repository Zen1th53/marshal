# ARTIFACTS.md — Artifact Registry

## Purpose

Track generated/build/release artifacts that matter to verification or delivery.

## Record

```yaml
id: ART-000
kind: binary | package | container | report | sbom | generated_source | dataset | archive
path_or_uri: unknown
source_commit: unknown
task_ids: []
producer:
  agent: unknown
  command: unknown
environment_ref: memory/ENVIRONMENT.md
hash:
  algorithm: sha256
  value: unknown
verification_refs: []
status: generated
created_at: null
expires_at: null
```

## Rule

An artifact without source provenance is not release evidence.

Do not store secrets or private production dumps in the general artifact registry.
