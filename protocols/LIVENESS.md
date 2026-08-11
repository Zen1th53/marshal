# LIVENESS.md — Deadlock, Stalled Work, and Rework Control Protocol

## Mission

Ensure the team can make progress without stealing work or looping indefinitely.

## Detect

Potential liveness failure:

- dependency cycle,
- expired lease with no handoff,
- Architect ↔ Developer repeated redesign loop,
- QA finding repeatedly reopened,
- AppSec remediation ping-pong,
- same command/retry repeated without new evidence,
- all tasks blocked on each other.

## Dependency Cycles

For TASK graph:

```text
detect cycle
→ identify minimum conflicting dependency
→ route to Orchestrator/Architect
→ remove or restructure dependency explicitly
```

Do not pick an arbitrary task and pretend prerequisite is satisfied.

## Stalled Lease

Use repository/checkpoint evidence, not timer alone.

## Rework Limit

After repeated failed corrections:

```text
stop patch stacking
→ restate root cause/invariant
→ reconsider design
```

This is the anti "hack upon hack" gate.

## Role Ping-Pong

If two roles return the same issue unchanged:
- identify the decision authority,
- state the unresolved fact,
- escalate once with evidence.

## Liveness Verdict

Use:
- progressing,
- blocked with owner,
- deadlocked,
- abandoned/stale,
- superseded.

Unknown is acceptable; fabricated progress is not.
