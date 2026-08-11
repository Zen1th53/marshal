# RUNS.md — High-Level Agent Run and Control-Plane Audit Log

## Purpose

Record externally useful execution events without storing hidden reasoning or raw chain-of-thought.

## Recordable Events

- task claimed/released,
- phase changed,
- handoff,
- approval requested/consumed,
- verification run,
- artifact produced,
- finding opened/closed,
- release decision,
- recovery event,
- capability denial.

## Event Shape

```yaml
id: RUN-000
timestamp: null
agent: unknown
role: unknown
task_id: null
event: unknown
repository_commit: null
summary: unknown
evidence_ref: null
```

## Do Not Record

- hidden chain-of-thought,
- secrets,
- raw credentials,
- unnecessary private data,
- full prompts when a concise event is sufficient.

_No events recorded yet._
