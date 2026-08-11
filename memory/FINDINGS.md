# FINDINGS.md — Open QA and AppSec Findings

## Purpose

Shared unresolved-defect queue.

QA owns QA findings.
AppSec owns security findings.
Other roles may not silently close or delete them.

Closed finding detail should remain in the original QA/AppSec report; only durable lessons should be promoted into `MEMORY.md`.

---

## Record Format

```markdown
## FIND-<id> — <title>

Owner: QA | AppSec
Severity: BLOCKER | HIGH | MEDIUM | LOW
Status: open | assigned | fixing | ready_for_retest | closed | accepted_risk
Assigned to:
Opened:
Updated:

### Boundary / Requirement
<what property is violated>

### Evidence
<exact reproducer/test/path>

### Impact
<what fails / what capability is gained>

### Required Fix Property
<what must become true; not a speculative patch>

### Required Verification
<test/procedure proving closure>

### Closure
<who verified, commit, command/procedure, result>
```

_No open findings._
