# Runtime Health and Readiness

## Health vs Readiness

### Liveness

Is the runtime process alive?

### Readiness

Can it safely accept new work?

A runtime can be live but not ready.

## Readiness Dependencies

Required for normal local mode:
- canonical store writable,
- policy engine available,
- repository reachable,
- task/worktree manager functional.

Optional degraded dependencies:
- semantic retrieval,
- graph retrieval,
- historical recall.

## Fail Closed

If policy/approval state cannot be verified:
- privileged operation is denied.

If semantic index is down:
- canonical operations can continue.

## Doctor Output

`agentctl doctor` should classify:

```text
PASS
DEGRADED
FAIL
```

and list exactly which capabilities are affected.
