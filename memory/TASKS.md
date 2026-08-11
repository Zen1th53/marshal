# TASKS.md — Shared Task Graph and Ownership State

## Purpose

This file tracks active engineering work as a dependency graph.

`STATE.md` answers:

```text
Where is the team now?
```

`TASKS.md` answers:

```text
What work exists?
Who owns each unit?
What depends on what?
Which unit is safe to execute now?
```

This file is coordination state, not product specification.

---

## Source-of-Truth Rule

Repository evidence, approved specifications, and current explicit user instructions outrank this file.

If a task record disagrees with reality:

```text
mark stale
→ inspect repository/spec
→ repair task record
→ continue
```

Do not make code conform to stale task metadata.

---

## Task States

Allowed:

```text
proposed
ready
claimed
working
blocked
review
qa
security_review
ready_to_merge
merged
cancelled
superseded
```

State transitions must be explainable.

Typical path:

```text
proposed
→ ready
→ claimed
→ working
→ review
→ qa/security_review
→ ready_to_merge
→ merged
```

---

## Canonical Task Record

```yaml
id: TASK-000
title: none
status: proposed
risk: R0

source:
  kind: user | spec | issue | incident | finding | decision
  reference: none

ownership:
  role: none
  agent_id: none
  claimed_at: null
  lease_until: null

repository:
  branch: null
  worktree: null
  base_commit: null
  head_commit: null

dependencies:
  requires: []
  blocks: []

scope:
  files: []
  components: []
  public_contracts: []
  data_migrations: []

acceptance:
  requirements: []
  evidence_required: []

review:
  architect: not_required
  qa: not_required
  appsec: not_required

blocking:
  reason: null
  finding_ids: []

next_action: none
updated_at: null
```

---

## Ownership Rule

One active task has one implementation owner.

The owner may collaborate, but there must be one role/agent responsible for advancing the task state.

Do not let two implementation agents independently modify the same task without explicit subdivision.

---

## Lease Rule

A claim is temporary ownership, not permanent lock.

A claim should record:

```text
agent
role
branch
worktree
base commit
lease expiry
```

If the lease expires:

```text
do not immediately steal
→ check current repository/worktree evidence
→ inspect last checkpoint/handoff
→ determine whether owner is actually inactive
→ reclaim explicitly
```

Wall-clock expiry alone is not proof that work is abandoned.

---

## Dependency Rule

A task is `ready` only when all hard dependencies are satisfied.

Hard dependency:

```text
TASK-A must be correct before TASK-B can be implemented safely
```

Do not invent dependencies merely because work is related.

Independent tasks should remain independent.

---

## Task Splitting Rule

Split a task when:

- two agents can work independently without shared mutable design state,
- one part can be reviewed/reverted independently,
- risk differs materially,
- one part is prerequisite architecture/migration,
- one diff would become not reviewable.

Do not split when it creates fake micro-tasks with no independent value.

---

## Merge Rule

A task may move to `ready_to_merge` only when required gates pass.

Examples:

### R1

```text
Developer evidence
+ QA PASS
```

### R2

```text
Architect gate if required
+ Developer evidence
+ QA PASS
+ AppSec if attack surface changed
```

### R3

```text
Architect gate
+ AppSec design gate
+ Developer evidence
+ QA PASS
+ AppSec PASS/PASS WITH ACCEPTED RISK
```

---

## Current Tasks

_No active tasks recorded yet._
