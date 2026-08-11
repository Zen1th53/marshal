# Runtime Event Bus

## Mission

Notify components about durable control-plane state changes without making the
event bus the only source of truth.

## Event Rule

Events describe committed facts.

Prefer:

```text
canonical transaction
→ durable outbox
→ publish
```

over:

```text
publish first
→ hope DB update succeeds
```

## Core Events

See `runtime/EVENTS.yaml`.

Examples:
- TASK_READY
- TASK_CLAIMED
- TASK_RELEASED
- TASK_BLOCKED
- HANDOFF_CREATED
- FINDING_OPENED
- FINDING_READY_FOR_RETEST
- QA_VERDICT_CHANGED
- APPSEC_GATE_CHANGED
- APPROVAL_REQUESTED
- APPROVAL_GRANTED
- ARTIFACT_REGISTERED
- HEAD_CHANGED
- VERIFICATION_INVALIDATED
- AGENT_HEARTBEAT_MISSED
- WORKER_EXITED

## Delivery

Consumers must tolerate duplicates.

Use stable `event_id`.

Critical state should be reconstructable from canonical store if events are replayed or lost.

## Ordering

Do not promise global ordering unless implemented.

Use aggregate/task revision when per-task order matters.
