# HANDOFF.md — Cross-Agent Handoff Protocol

## Goal

Transfer enough context for the next role to continue without rediscovering core facts or guessing decisions.

A handoff is not a status essay.

---

## Required Format

```markdown
# Handoff: <short title>

## Context
Why the work exists.

## Scope
Exact components/files/interfaces in scope.

## Out of Scope
Explicit exclusions.

## Invariants
Properties that must remain true.

## Decisions
Only decisions already made.

## Changed / Reviewed Areas
Concrete paths/components.

## Risks
Known correctness, security, migration, or operational risks.

## Verification Performed
Exact commands/procedures and results.

## Verification Not Performed
Anything relevant that remains unverified.

## Open Issues
Only genuine unresolved issues.

## Requested Next Role Action
Specific ask to the next agent.
```

---

## Handoff Rules

1. Separate fact from assumption.
2. Do not claim unrun verification.
3. Do not bury BLOCKER/HIGH risk.
4. Include exact interface names/paths when known.
5. Keep non-goals explicit.
6. Include migration state when applicable.
7. Include security-sensitive surface changes.
8. Do not send “please review everything.”

---

## Role-Specific Additions

### Architect → Developer

Must include:
- component ownership,
- contracts,
- state model,
- invariants,
- failure behavior,
- migration order,
- compatibility constraints.

### Developer → QA

Must include:
- changed behavior,
- test commands,
- edge/failure cases,
- fixture/setup needs,
- risk areas.

### Developer → AppSec

Must include:
- entry points,
- auth/authz path,
- untrusted inputs,
- file/network/process capability,
- dependencies,
- rendering changes,
- secrets/data classification.

### QA → Developer

For failures:
- minimal reproducer,
- expected vs actual,
- evidence,
- severity,
- regression scope.

### AppSec → Developer

For findings:
- boundary,
- preconditions,
- impact,
- evidence,
- root cause,
- minimal robust fix,
- regression expectation.

---

## Reject a Handoff If

- it contains only conclusions,
- verification is vague,
- scope is undefined,
- required decision belongs to the next role but is hidden as assumption,
- blocking risk is omitted,
- implementation must guess an architectural invariant.
