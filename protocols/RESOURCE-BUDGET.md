# RESOURCE-BUDGET.md — Token, Tool, Compute, and Parallelism Protocol

## Mission

Use resources deliberately.

## Progressive Spend

Prefer:

```text
cheap/local/exact
→ targeted
→ expensive/broad
```

Examples:
- exact repository search before semantic history search,
- targeted test before full suite,
- local static check before remote heavy scan when equivalent,
- one relevant reference repo before ten.

## Never Trade Correctness for Appearance

If budget prevents required verification:

```text
mark UNVERIFIED / BLOCKED
```

Do not report PASS.

## Retry Budget

Retries require a reason.

Do not spend budget on repeated identical failing operations without new evidence.

## Parallelism Budget

More agents are not automatically faster.

Parallelize only independent work with clear ownership.

## Context Budget

Use `AGENT-MANIFEST.yaml` and `protocols/CONTEXT-LOADING.md`.

Loading every role/protocol/memory file is a failure mode, not thoroughness.
