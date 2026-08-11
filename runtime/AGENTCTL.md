# agentctl Command Contract

## Mission

One operator-facing entrypoint for inspecting and controlling the runtime.

## Core Commands

```bash
agentctl status
agentctl doctor
agentctl agents
agentctl tasks
agentctl task show TASK-123
agentctl task claim TASK-123
agentctl task release TASK-123
agentctl handoff TASK-123 developer qa
agentctl checkpoint TASK-123
agentctl approvals
agentctl approval show APR-12
agentctl artifacts
agentctl artifact show ART-44
agentctl memory recall "query"
agentctl events tail
agentctl validate
```

## Status

`agentctl status` should show:

- runtime mode,
- repository,
- current branch/HEAD,
- active task,
- agents/heartbeats,
- open blockers,
- open findings,
- pending approvals,
- scheduler health,
- canonical store health.

## Doctor

`agentctl doctor` verifies:

- pack version/schema,
- repository policy presence,
- SQLite/store access,
- worktree support,
- runtime directory permissions,
- artifact directory,
- optional adapter health.

It must not silently repair destructive issues.

## Exit Codes

Recommended:

```text
0 success
1 generic failure
2 invalid usage
3 policy denied
4 approval required
5 conflict/stale state
6 unavailable dependency
7 verification failure
```

## JSON Mode

Every read command should support structured output:

```bash
agentctl --json status
```

CLI text is presentation; JSON/API schema is the stable automation contract.

## Safety

Commands that can:
- deploy,
- delete,
- rotate/revoke secrets,
- rewrite shared history,
- perform destructive migrations

must route through policy + approval.

No hidden "force" flag may bypass required authorization.
