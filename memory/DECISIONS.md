# DECISIONS.md — Accepted Durable Decisions

## Rule

This is not brainstorming.

Record a decision only after the role with authority has accepted it.

Preserve superseded decisions for audit/history instead of rewriting history.

---

## Record Format

```markdown
## DEC-<id> — <short title>

Status: proposed | active | superseded | rejected
Owner: Architect | AppSec | User/Owner | Team
Date:
Supersedes:
Superseded by:

### Decision
<one precise statement>

### Reason
<why this shape was chosen>

### Invariants
- ...

### Consequences
- ...

### Alternatives Rejected
- <option> — <specific reason>

### Evidence / Source
- <spec/ADR/path/commit/test>

### Revalidation Trigger
<what future change should reopen this decision>
```

_No decisions recorded yet._
