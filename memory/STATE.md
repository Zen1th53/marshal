# STATE.md — Current Shared Team State

## Purpose

This is the compact, authoritative *working-memory* view for the current task.

A new agent should be able to read this file and answer:

- What project am I in?
- What task is active?
- Which phase are we in?
- What has each role completed?
- What is blocked?
- What happens next?
- Which repository commit was last verified?

Keep this file small. It is not history.

---

## Source-of-Truth Rule

Fresh repository/runtime evidence outranks this file.

If recorded branch/commit differs materially from the current repository, mark the state stale and revalidate before relying on it.

---

## Project

```yaml
project:
  name: unknown
  repository: unknown
  branch: unknown
  worktree: unknown
  commit: unknown
```

## Active Task

```yaml
task:
  id: none
  title: none
  source: none
  risk: unknown
  phase: idle
  status: waiting_for_task
  started_at: null
  updated_at: null
```

Allowed `phase` values:

```text
idle
discovery
architecture
security_design
implementation
qa
security_verification
release
blocked
complete
```

## Agent Status

```yaml
orchestrator:
  status: idle
  current_action: waiting_for_task
  blocker: null
  last_handoff: null

architect:
  status: idle
  current_action: none
  output: null
  blocker: null

developer:
  status: idle
  current_action: none
  branch: null
  commit: null
  last_verified: null
  blocker: null

qa:
  status: idle
  current_action: none
  verdict: null
  blocker: null

appsec:
  status: idle
  current_action: none
  gate: null
  blocker: null
```

Allowed agent `status`:

```text
idle
working
waiting
blocked
complete
```

## Current Invariants

```text
None.
```

## Current Blockers

```text
None.
```

## Next Action

```text
User provides a concrete task.
```

## Verification Snapshot

This is a pointer, not proof by itself.

```yaml
verification:
  commit: null
  command: null
  result: null
  scope: null
  timestamp: null
```

If current HEAD differs from `verification.commit`, treat the verification snapshot as stale until rerun.
