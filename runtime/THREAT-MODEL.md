# Runtime Threat Model

## Assets

- repository write authority,
- task ownership,
- approvals,
- secrets,
- production credentials,
- artifacts,
- audit records,
- canonical memory,
- release authority.

## Actors

- normal agent,
- compromised/malicious agent session,
- untrusted repository content,
- untrusted external reference/web content,
- operator,
- compromised dependency/tool,
- stale historical memory.

## Entry Points

- agentctl,
- Runtime API/MCP,
- worker tool requests,
- repository content,
- retrieved memory,
- event consumers,
- secret requests,
- artifact uploads.

## Major Abuse Cases

### Policy bypass through alternate tool

Control:
- semantic capability policy, not command-name-only rules.

### Prompt injection from repository/web/memory

Control:
- `protocols/INSTRUCTION-TRUST.md`,
- trusted policy separate from retrieved content.

### Task theft after heartbeat miss

Control:
- lease + worktree/checkpoint inspection before reclaim.

### Secret exfiltration

Control:
- scoped lease,
- no memory/log persistence,
- external-upload policy.

### Artifact substitution

Control:
- digest + source provenance + immutable storage.

### Event forgery/replay

Control:
- authenticated runtime producer,
- stable IDs,
- canonical revision,
- idempotent consumers.

### Stale verification used after HEAD changes

Control:
- HEAD_CHANGED → verification invalidation.

### Runtime DB corruption

Control:
- backup/restore,
- transactions,
- schema versioning,
- canonical reconciliation with repository evidence.

## Security Principle

The runtime enforces the pack; it must therefore be treated as privileged
engineering infrastructure.
