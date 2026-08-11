# WORKTREE.md — Branch and Worktree Isolation Protocol

## Mission

Make parallel agent work reviewable, reversible, and non-destructive.

---

## 1. Default Rule

For non-trivial parallel work:

```text
one implementation task
→ one branch
→ one worktree
→ one active owner
```

Shared working directories are the exception, not the default.

---

## 2. Before Creating a Worktree

Verify:

```text
repository clean enough to branch safely
task is claimed
base branch identified
base commit recorded
branch name does not collide
worktree path does not collide
```

Do not move or delete another agent's worktree.

---

## 3. Naming

Prefer predictable names:

```text
branch:
agent/<task-id>-<short-slug>

worktree:
.worktrees/<task-id>-<short-slug>
```

Follow repository policy if it defines naming.

---

## 4. Base Commit

Every task must record its base commit.

This makes it possible to answer:

```text
What changed because of this task?
```

Do not use a vague branch name as the only provenance.

---

## 5. Dirty Worktree Rule

Before handoff/review:

```text
git status
```

must be inspected.

Uncommitted state must be:

- intentionally included,
- committed,
- checkpointed,
- or explicitly reported.

Do not hand off mysterious dirty files.

---

## 6. Commit Rule

Follow `TORVALDS.md`.

One logical change per commit.

Do not mix:

```text
refactor + behavior
formatting + feature
dependency cleanup + bugfix
migration + unrelated API cleanup
```

Each commit should be independently understandable and preferably build/test independently.

---

## 7. Rebase / Merge

Before rebasing or merging another agent's active branch:

```text
confirm ownership state
→ confirm handoff
→ inspect dirty state
→ preserve evidence
```

Do not rewrite another active agent's history.

---

## 8. Force Push

Default:

```text
forbidden on shared/reviewed branches
```

Allow only when repository policy explicitly permits it and no dependent agent/reviewer relies on the old history.

If force update is unavoidable:

- record old head,
- record new head,
- notify dependent owners,
- invalidate stale verification.

---

## 9. Conflict Resolution

A merge conflict is not permission to choose whichever side compiles.

Resolve by:

```text
identify invariant
→ identify semantic owner
→ inspect both changes
→ choose or redesign based on requirement
→ rerun affected tests
```

If conflict reveals design disagreement:

```text
Developer → Architect
```

---

## 10. Verification Invalidation

Any history-changing operation or conflict resolution may invalidate old evidence.

After:

- rebase,
- merge conflict resolution,
- cherry-pick with edits,
- dependency lock reconciliation,

rerun verification affected by the changed code.

Do not reuse a PASS from a different tree state.

---

## 11. Worktree Cleanup

Delete a worktree only when:

```text
task ownership released
important commits reachable
handoff complete
uncommitted data absent or preserved
```

Never use cleanup as a substitute for understanding unknown files.

---

## 12. Merge Gate

Before merge:

```text
[ ] task ready_to_merge
[ ] branch/head recorded
[ ] required QA/AppSec gates current for this head
[ ] final diff review complete
[ ] merge target current enough for policy
[ ] conflicts resolved semantically
[ ] required post-merge verification identified
```

---

## 13. Post-Merge

After merge:

- record merged commit,
- update TASKS/STATE,
- invalidate branch-specific current-state claims,
- unblock dependents,
- preserve only durable lessons.

Worktree state is ephemeral; decisions and evidence are durable.
