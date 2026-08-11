# TRACEABILITY.md — Requirement-to-Release Links

## Purpose

Preserve a minimal chain from why a change exists to the evidence that released it.

## Canonical Chain

```text
REQ
→ DEC/ADR/DESIGN
→ TASK
→ COMMIT
→ TEST/EVIDENCE
→ ARTIFACT
→ RELEASE
```

Not every trivial change needs every node.

## Record

```yaml
requirement_id: REQ-000
source: unknown
decision_ids: []
task_ids: []
commits: []
test_evidence: []
artifact_ids: []
release_ids: []
status: active
```

## Rule

Traceability is for reversibility and audit, not paperwork.

Create links when losing them would make it hard to answer:

- why does this code exist?
- which requirement did this test prove?
- which commit built this artifact?
- which release contains the fix?
