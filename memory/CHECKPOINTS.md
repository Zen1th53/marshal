# CHECKPOINTS.md — Resume and Risk Checkpoints

Create a checkpoint only when resuming later would otherwise require expensive rediscovery.

Good checkpoint moments:

- before risky migration,
- before session/model switch,
- before primary-agent ownership transfer,
- before large QA/AppSec rework,
- before release when rollback context matters.

Do not checkpoint every edit.

---

## Record Format

```markdown
## CHK-<id>

Task:
Phase:
Timestamp:
Repository commit:
Branch/worktree:

### Agent State
- Orchestrator:
- Architect:
- Developer:
- QA:
- AppSec:

### Invariants
- ...

### Completed
- ...

### Open Findings
- ...

### Current Evidence
- ...

### Next Action
- ...

### Resume Notes
<only non-obvious facts>
```

_No checkpoints recorded yet._
