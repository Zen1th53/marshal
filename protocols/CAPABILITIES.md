# CAPABILITIES.md — Agent Capability and Permission Protocol

## Mission

Separate what an agent can technically do from what it is authorized to do.

`CAPABILITIES.yaml` is the default reusable policy. Repository-local policy may narrow or explicitly widen it.

## Rules

1. Tool availability is not permission.
2. Task scope limits write authority.
3. Dangerous operations require `protocols/APPROVAL.md`.
4. Missing permission is a STOP condition, not an invitation to work around controls.
5. Secrets and production access are denied by default.
6. Role authority and tool permission are separate checks.

## Decision Flow

```text
need operation
→ is it required by task?
→ does role own the decision?
→ does capability policy allow it?
→ does repository policy narrow it?
→ does operation require approval?
→ execute only when all required gates pass
```

## Scope Binding

Write/execute permission should bind to:

- task ID,
- repository,
- branch/worktree,
- environment,
- resource or path,
- approval ID when required.

Broad "write access" must not be interpreted as permission to change unrelated files.

## Privilege Minimization

Prefer:

```text
read-only
> task-scoped write
> component-scoped mutation
> repository-wide mutation
> production mutation
```

Grant only the level the task actually needs.

## Escalation

If implementation requires privilege outside the current policy:

```text
STOP
→ describe operation
→ explain why required
→ identify blast radius
→ request correct owner/approval
```

Do not disable a guardrail or use a different tool to bypass the policy.
