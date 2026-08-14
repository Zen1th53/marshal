# Runtime Reconciliation Contract

## Canonical Mode

Runtime startup must declare:

```text
file-first
or
runtime-first
```

Do not infer dynamically per write.

## Runtime-First Export

```text
DB transaction
→ stable snapshot revision
→ export Markdown
→ record exported revision
```

## File-First Import

```text
read Git-backed state
→ validate IDs/schema
→ compare runtime revision
→ transactional import
```

## Split-Brain Detection

Trigger on:

- task ID/status mismatch,
- HEAD mismatch,
- owner/lease mismatch,
- finding status mismatch,
- approval mismatch.

## Operations

Recommended:

```text
marshal diff-state
marshal export-state
marshal import-state
marshal reconcile
```

Every resolving mutation should be audit logged.
