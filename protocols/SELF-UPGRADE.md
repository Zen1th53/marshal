# SELF-UPGRADE.md — Pack Update Protocol

## Rule

An agent may inspect or prepare an upgrade.

It must not silently replace its own governance while executing a task.

## Commands / Desired Runtime Surface

```text
agentctl pack status
agentctl pack diff <new>
agentctl pack verify <new>
agentctl pack upgrade <new>
agentctl pack rollback
```

## Gate

Upgrade requires:

- version/manifest verification,
- changelog review,
- migration review,
- project override preservation,
- conformance PASS,
- approval if governance policy requires.

## Self-Modification Hazard

The currently executing agent must not weaken the rules that govern its own
current task to make that task easier.

Governance change and product change should be separate reviewable work.
