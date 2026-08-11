# SCHEMA.md — Agent-Agnostic Memory Contract

## Goal

Start with human-readable Markdown files and allow the same semantics to move later behind SQLite, MCP, TurboVec, Cognee, Memoria, or another backend.

The memory **core contract** must not depend on any one agent or storage engine.

---

## 1. Memory Classes

### Working

Current task state.

Canonical surfaces:

```text
STATE.md
HANDOFFS.md
CHECKPOINTS.md
```

### Decision

Accepted choices with durable consequences.

Canonical surfaces:

```text
DECISIONS.md
ADRs
```

### Finding

Open QA/AppSec defects.

Canonical surface:

```text
FINDINGS.md
```

### Semantic

Durable project facts and lessons.

Canonical surface:

```text
MEMORY.md
```

### Episodic

Historical sessions/events.

Canonical source:

```text
external/session-history adapter when available
```

Do not inject all episodic history into every context.

### Procedural

Reusable workflows.

Canonical sources:

```text
agents/protocols/
project runbooks/docs
```

---

## 2. Canonical Machine Record

Future backends should preserve:

```yaml
id: string
project_id: string
memory_type: working | decision | finding | semantic | episodic | procedural
title: string
body: string
status: active | stale | superseded | rejected | closed
confidence: verified | observed | inferred | unverified

source:
  kind: repository | user | test | runtime | agent_handoff | external
  reference: string

provenance:
  agent: orchestrator | architect | developer | qa | appsec | external
  session_id: string | null
  branch: string | null
  worktree: string | null
  commit: string | null

created_at: timestamp
updated_at: timestamp
last_verified_at: timestamp | null
supersedes: [id]
tags: [string]
acl_scope: string
```

---

## 3. Stable Identity

Use stable IDs.

Recommended prefixes:

```text
MEM-   semantic durable memory
DEC-   decision
FIND-  QA/AppSec finding
HO-    handoff
CHK-   checkpoint
EP-    episodic/session record
```

Do not use mutable titles or list positions as identity.

---

## 4. Provenance

Every durable record must answer:

```text
Who wrote this?
From what evidence?
Against which branch/commit?
When was it last verified?
```

Unknown provenance lowers trust.

---

## 5. Staleness

A memory may become stale when:

- referenced file changed,
- repository moved beyond the verified commit,
- dependency/version changed,
- task semantics changed,
- decision was superseded,
- user changed the requirement.

Never represent "not checked" as "unchanged."

---

## 6. Conflict

Contradictory durable memories must not be auto-merged.

Use:

```text
detect
→ preserve both
→ mark conflict
→ inspect provenance
→ route to authority
→ accept/reject/supersede explicitly
```

Latest timestamp alone is not truth.

---

## 7. Retrieval Scope

Retrieval should support filters such as:

```text
project
task
role
branch/worktree
status
security classification
tags
```

Security-sensitive memory may require stricter ACLs.

Never index secrets into vector/semantic memory.

---

## 8. Retrieval Ranking

A future retrieval engine may combine:

```text
exact identifiers
lexical/BM25
semantic/vector similarity
graph relationships
current-files relevance
task/role scope
recency
accepted/rejected/superseded status
```

Results must return provenance and staleness.

---

## 9. Write Semantics

File-backed automated writes:

```text
read current revision
→ validate
→ write sibling temp
→ flush
→ atomic rename
```

Machine-backed writes should be transactional.

Avoid partial shared-state writes.

---

## 10. Checkpoint

Canonical checkpoint includes:

```yaml
task_id:
phase:
repository_commit:
branch:
worktree:
agent_status:
active_decisions:
open_findings:
verification_snapshot:
next_action:
timestamp:
```

A checkpoint proves only that coordination state was captured.

It does not prove code correctness.

---

## 11. Agent Adapters

Target adapters may include:

```text
Gemini CLI
Codex
Claude Code
OpenCode
Crush
Aider
custom MCP clients
```

The memory core must not require Claude-specific hooks or any single harness lifecycle.

---

## 12. Logical Memory API

```text
memory.status()
memory.recall(query, scope)
memory.remember(record)
memory.update(id, patch)
memory.supersede(old_id, new_id)
memory.find_conflicts(scope)
memory.checkpoint(task)
memory.handoff(from, to, task)
memory.audit(id)
```

Role authority must be enforced on write operations.

Developer cannot close an AppSec finding merely by editing storage.
