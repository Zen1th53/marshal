# Worker Protocol

## Lifecycle

```text
REGISTER
→ ASSIGN
→ PREPARE
→ RUN
→ HEARTBEAT
→ CHECKPOINT/HANDOFF
→ VERIFY
→ RELEASE
→ EXIT
```

## Assignment

Worker receives:

- task ID,
- role,
- repository/worktree,
- base/head commit,
- allowed capabilities,
- loaded context manifest,
- relevant memory state,
- approval references,
- budget.

## Prepare

Worker must:
- verify current HEAD/worktree,
- verify task ownership,
- load required role/protocol context,
- verify environment where relevant.

## Heartbeat Payload

Should contain only operational state:

```yaml
session_id:
task_id:
phase:
head_commit:
status:
timestamp:
```

No chain-of-thought.

## Completion

Worker may request completion, but runtime/QA gates determine final state.

Worker completion request includes:
- changed commit,
- evidence,
- not-verified scope,
- handoff target.

## Crash

Crash triggers:
- session stale/suspect,
- secret revocation,
- preservation of worktree,
- scheduler re-evaluation.

It must not delete the worktree or reassign immediately without inspection.
