# MEMORY.md — Durable Project Memory

## Purpose

Store stable project knowledge that should survive tasks, sessions, agents, and model changes.

This is **not** a transcript, task log, or current status page.

Good durable memory:

- stable architecture facts,
- important project conventions,
- recurring environment constraints,
- verified external quirks,
- durable lessons learned,
- rejected approaches worth not rediscovering.

Bad durable memory:

- "Developer is editing file X",
- temporary test output,
- speculative hypotheses,
- raw chat transcripts,
- transient blockers,
- secrets.

Working state belongs in `STATE.md`.

---

## Precedence

```text
fresh repository/runtime evidence
> explicit current user/task requirement
> approved spec / ADR
> current STATE.md
> active DECISIONS.md
> durable MEMORY.md
> historical/session recall
> agent recollection
```

Memory is an optimization, not authority over reality.

---

## Stable Project Facts

_No durable facts recorded yet._

---

## Stable Engineering Conventions

_No durable conventions recorded yet._

---

## External Constraints / Quirks

_No durable external constraints recorded yet._

---

## Lessons Learned

Use:

```markdown
## MEM-<id> — <short title>

Status: active | stale | superseded
Recorded:
Last verified:
Evidence:

### Fact
<verified durable fact>

### Why It Matters
<future engineering consequence>

### Revalidation Trigger
<what should cause this memory to be checked again>
```

_No lessons recorded yet._

---

## Rejected Approaches

Use:

```markdown
## REJECT-<id> — <approach>

Status: active | superseded
Decision/Evidence:

### Why Rejected
<specific failure mode>

### Preferred Replacement
<what to use instead>
```

_No rejected approaches recorded yet._
