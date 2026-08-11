# TRACEABILITY.md — Change Traceability Protocol

## Mission

Keep enough linkage to reconstruct intent, implementation, evidence, and delivery.

## Minimum for R1+

```text
task
→ commit(s)
→ verification
```

## Minimum for R2/R3

```text
requirement
→ design/decision
→ task(s)
→ commit(s)
→ QA/AppSec evidence
→ artifact/release
```

## Rules

- Use stable IDs.
- Do not encode traceability only in prose.
- Do not invent IDs for external systems if the repository already has canonical issue/PR IDs.
- One commit may support multiple tasks only when the change is genuinely shared.
- One task may have multiple commits when each remains logically atomic.

## Regression Fix

For a bug/security fix, preserve:

```text
finding/bug
→ root-cause task
→ regression test
→ fix commit
→ verification
```

## Removal

When code is deleted, traceability should allow the reviewer to determine which old decision/requirement is being retired or superseded.
