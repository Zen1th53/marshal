# Runtime Policy Engine

## Mission

Enforce the pack's role/capability/approval rules at the tool boundary.

## Input

```yaml
subject:
  agent_id: AGENT-...
  session_id: SESSION-...
  role: developer

task:
  id: TASK-...
  risk: R2
  owner: AGENT-...

operation:
  action: git.commit
  target: repository/path

environment:
  name: local
  production: false

capability:
  requested: filesystem_write

approval:
  id: null
```

## Output

```yaml
decision: allow | deny | require_approval
reason: ...
policy_rule: ...
constraints: []
```

## Rules

Policy evaluates:

1. repository-local policy,
2. runtime capability policy,
3. role authority,
4. task ownership/scope,
5. environment,
6. dangerous-operation classification,
7. approval validity.

## Deny by Default

For:
- production mutation,
- secret access,
- history rewrite,
- external upload,
- destructive operations.

## No Bypass by Alternate Tool

If `git.force_push` is denied, using shell, library, or direct protocol to achieve
the same semantic operation is also denied.

Policy is about capability semantics, not command names.

## Audit

Every deny/require-approval decision should emit an audit event when material.

Do not log secret values.
