# RECONCILIATION.md — File State vs Runtime State Protocol

## Mission

Prevent split-brain between human-readable Markdown state and an executable
runtime database.

## Modes

### File-first

Markdown/Git is canonical.

Runtime, if present, is an ephemeral/derived view.

### Runtime-first

Runtime DB is canonical for live coordination.

Markdown files are exports/checkpoints.

## Forbidden

```text
two independent canonical writers
```

without explicit conflict semantics.

## Reconcile

Compare at least:

- project/repository identity,
- branch,
- commit,
- active task,
- task status/phase,
- owner/lease,
- open findings,
- approvals.

## Conflict

```text
detect
→ freeze affected mutation
→ identify canonical mode
→ compare provenance/revisions
→ resolve by authority
→ write reconciliation record
```

Do not use latest timestamp alone as truth.

## CLI

The lightweight helper supports state comparison:

```bash
python tools/agentos.py reconcile-state \
  --file-state state.json \
  --runtime-state runtime.json
```

A full runtime should expose equivalent `agentctl diff-state` / `reconcile`.
