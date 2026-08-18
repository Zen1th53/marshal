# Provenance and Chain-of-Custody (T07)

The `provenance` package tracks task -> agent -> context -> tool calls -> patch -> verification -> approval -> commit as an immutable chain of custody.

## Key APIs

- `Begin(ctx, changeID, taskID, agentID, provider, contextDigest, patchDigest)`
- `AttachToolCall(ctx, changeID, toolCallID)`
- `AttachEvidence(ctx, changeID, evidenceID)`
- `AttachApproval(ctx, changeID, approvalID)`
- `Seal(ctx, changeID, commitSHA)`
- `Trace(ctx, changeID)`
