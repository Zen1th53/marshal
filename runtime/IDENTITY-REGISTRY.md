# Agent Identity Registry

## Mission

Identify which concrete agent/session is acting, not only which abstract role exists.

## Agent Record

```yaml
agent_id: AGENT-...
display_name: gemini-dev-02
role: developer
model_provider: optional
model_name: optional
capabilities: []
status: registered
created_at: ...
```

## Session Record

```yaml
session_id: SESSION-...
agent_id: AGENT-...
repository: ...
branch: ...
worktree: ...
task_id: ...
started_at: ...
last_heartbeat: ...
status: active
```

## Heartbeat

Heartbeat indicates liveness, not correctness.

A missing heartbeat means:

```text
session may be stale
```

It does not mean:

```text
task is safe to steal
```

Reclaim requires:
- lease state,
- worktree state,
- checkpoint/handoff,
- repository evidence.

## Identity Binding

Every privileged runtime request binds:
- agent ID,
- session ID,
- role,
- task,
- target operation.

Do not authorize only from a user-controlled role string.

## Rotation

Sessions are short-lived.

Agent identity may persist across sessions; task lease does not.
