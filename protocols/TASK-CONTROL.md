# TASK-CONTROL.md — Multi-Agent Task Ownership Protocol

## Mission

Prevent duplicate work, hidden dependencies, stale ownership, and agents overwriting each other's in-progress changes.

---

## 1. Startup

Before beginning implementation:

```text
read memory/TASKS.md
→ identify task
→ inspect dependencies
→ inspect current owner
→ inspect branch/worktree
→ inspect repository HEAD
→ claim task
```

Do not edit code before the ownership state is coherent.

---

## 2. Claim

A valid claim records:

```text
task id
agent id
role
branch
worktree
base commit
claim timestamp
lease
```

Claim only a `ready` task.

Do not claim:
- blocked task,
- task owned by an active agent,
- task whose prerequisite is incomplete,
- vague task with no acceptance criteria.

---

## 3. Ownership Collision

If another agent appears to own the task:

```text
STOP
→ inspect TASKS
→ inspect HANDOFFS/CHECKPOINTS
→ inspect branch/worktree
→ inspect recent commits
```

Then choose one:

```text
owner active       → do not interfere
owner finished     → require handoff/release
owner stale        → explicit reclaim
work subdividable  → create child tasks
```

Never silently edit another agent's working tree.

---

## 4. Lease Renewal

Renew ownership only when there is evidence of active progress:

- new checkpoint,
- updated head commit,
- verified investigation,
- explicit blocker update,
- handoff preparation.

Do not keep a task leased indefinitely with no evidence.

---

## 5. Release

Release ownership when:

- handoff occurs,
- task is blocked and another role owns next action,
- implementation finishes,
- task is cancelled/superseded.

A release must leave:

```text
repository commit
current status
verification performed
verification not performed
next owner/action
```

---

## 6. Parallel Work

Parallelize only when:

```text
independent inputs
+ independent mutable files/state
+ independent acceptance
+ merge order understood
```

Good:

```text
QA prepares acceptance matrix
AppSec reviews threat model
```

Good:

```text
Developer A changes backend parser
Developer B changes unrelated docs tooling
```

Bad:

```text
Developer A and Developer B both redesign the same schema
```

Bad:

```text
two agents change the same migration sequence independently
```

---

## 7. Parent / Child Tasks

A parent may represent a deliverable.

Children represent independently reviewable units.

Example:

```text
TASK-100 Dynamic Knowledge Tree

├── TASK-101 hierarchy schema
├── TASK-102 read-only public query
├── TASK-103 admin reorder UI
├── TASK-104 QA regression matrix
└── TASK-105 AppSec public/admin review
```

The parent is not merged merely because one child is complete.

---

## 8. Blocking

A blocker must name:

```text
fact
owner
required resolution
affected tasks
evidence
```

Bad:

```text
blocked by backend
```

Good:

```text
TASK-102 blocked because hierarchy migration TASK-101 has not defined
cycle-prevention semantics. Architect owns the decision.
```

---

## 9. Task Mutation Authority

Orchestrator owns:
- task graph,
- overall status,
- role assignment.

Assigned owner owns:
- implementation progress,
- branch/worktree/head pointers.

QA/AppSec own their finding links and gate status.

One role must not falsify another role's task gate.

---

## 10. Stale Task Detection

Treat a task record as stale when:

- recorded branch no longer exists,
- recorded HEAD materially differs,
- task was superseded,
- dependency changed,
- spec changed,
- owner handoff contradicts task state.

Repair metadata before relying on it.

---

## 11. Completion Gate

Before closing a task:

```text
[ ] dependencies satisfied
[ ] final owner released
[ ] required review gates passed
[ ] branch/head recorded
[ ] acceptance mapped to evidence
[ ] findings linked/closed or accepted
[ ] merge/release state recorded
[ ] next dependent tasks unblocked
```
