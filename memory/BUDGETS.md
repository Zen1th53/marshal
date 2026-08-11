# BUDGETS.md — Resource and Cost Policy

## Purpose

Bound expensive agent behavior without sacrificing correctness.

## Defaults

```yaml
context:
  policy: progressive
  load_full_repository: false
  load_full_memory_history: false

parallelism:
  policy: independent_tasks_only
  max_unspecified: conservative

external_research:
  policy: task_relevant_only

expensive_scans:
  policy: risk_based

retries:
  policy: bounded_and_root_cause_aware

budgets:
  token: repository_or_owner_defined
  time: repository_or_owner_defined
  compute: repository_or_owner_defined
  external_cost: repository_or_owner_defined
```

## Rule

Budget exhaustion lowers scope or requires escalation; it does not justify false PASS.
