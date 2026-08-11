# Remote Agent Interoperability

## Boundary

Remote agent collaboration uses A2A when available.

Tool/context integration uses MCP.

The Agent OS runtime remains authoritative for:
- local task ownership,
- approvals,
- role authority,
- tenant/project scope,
- artifact trust,
- release gates.

## Remote Delegation

```text
local TASK child
→ remote agent discovery
→ protocol/version negotiation
→ policy/data check
→ A2A task/message
→ remote artifact/status
→ validation
→ local evidence/handoff
```

Do not expose internal canonical memory wholesale to a remote agent.

Share only the scoped task context required.

## Remote Failure

Remote timeout or disconnect:
- preserve local task ownership state,
- mark remote child status,
- do not infer completion,
- retry only according to liveness/retry policy.
