# BACKEND.md — Shared Memory Backend Architecture

## Purpose

Define how the file-first memory layer may evolve into a real multi-agent service without changing role semantics.

---

## Canonical Principle

Retrieval technology is not truth.

Canonical engineering records are structured and provenance-bearing.

Recommended evolution:

```text
              Agent clients
      Gemini / Codex / Claude / others
                    │
                    ▼
              Memory API / MCP
                    │
         ┌──────────┴──────────┐
         │                     │
   Canonical Store       Retrieval Adapters
   SQLite/Postgres       ┌──────┼─────────┐
                         │      │         │
                      lexical TurboVec  Cognee

                  Historical Adapter
                        Deja Vu
```

---

## Canonical Store

Owns:

```text
projects
tasks
agents
leases
decisions
findings
handoffs
checkpoints
approvals
memory records
audit events
```

This layer determines record identity and lifecycle.

---

## Retrieval Adapters

### Exact / lexical

Use for:
- IDs,
- file paths,
- symbol names,
- exact errors.

### Semantic / TurboVec-style

Use for:
- concept similarity,
- prior investigations,
- related lessons.

### Graph / Cognee-style

Use for:
- task → decision → file → finding → test relationships.

### Episodic / Deja Vu-style

Use for:
- previous agent sessions,
- historical debugging trails,
- prior commands and touched files.

No adapter may silently mutate canonical truth.

---

## Storage V1

File-backed Markdown is valid for:

- one machine,
- low write concurrency,
- transparent debugging,
- Git-auditable history.

---

## Storage V2

SQLite is appropriate when:

- multiple records are updated frequently,
- task leases need atomicity,
- structured queries matter,
- one-host concurrent agents exist.

Use:
- transactions,
- foreign keys,
- WAL where appropriate,
- explicit schema migrations.

---

## Storage V3

Postgres becomes reasonable when:

- multiple machines,
- higher concurrency,
- central service,
- durable server-side arbitration

are real requirements.

Do not deploy it merely for prestige.

---

## MCP Boundary

Possible tools:

```text
team_memory_status
team_memory_task_list
team_memory_task_claim
team_memory_task_release
team_memory_recall
team_memory_remember
team_memory_decision
team_memory_finding
team_memory_handoff
team_memory_checkpoint
team_memory_approval
team_memory_audit
```

Write methods must enforce role authority.

---

## Conflict Model

For mutable structured state:

```text
revision
+ compare-and-swap
+ transaction
```

For durable semantic memory:

```text
preserve conflicting records
→ route to authority
→ supersede explicitly
```

Do not use timestamp-only last-write-wins for engineering truth.

---

## Security

Memory service must support:

- project isolation,
- role-aware writes,
- scoped reads,
- provenance,
- audit trail,
- secret rejection/redaction,
- bounded retrieval,
- no execution of retrieved content.

---

## Failure Behavior

If semantic/graph/history adapters fail:

```text
canonical store remains usable
```

If canonical store is unavailable:

```text
do not fabricate current state
→ fail loudly
→ use verified local fallback/checkpoint only if policy allows
```

---

## Migration Rule

Backend changes must preserve:

- stable record IDs,
- provenance,
- decision lifecycle,
- findings ownership,
- task ownership history,
- checkpoint lineage.

Changing storage technology must not silently change memory semantics.
