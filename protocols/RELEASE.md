# RELEASE.md — Release Gate Protocol

## Goal

Release only when required behavior, security, migration, and rollback evidence are explicit.

---

## Release Inputs

- approved requirement/spec,
- final implementation diff,
- QA verdict,
- AppSec gate if relevant,
- migration status,
- build/package artifacts,
- rollback procedure.

---

## Release Checklist

### Code
```text
[ ] final diff reviewed
[ ] no unrelated changes
[ ] build/package succeeds
[ ] repository quality gates pass
```

### QA
```text
[ ] acceptance verified
[ ] regression scope verified
[ ] no unresolved BLOCKER/HIGH functional defects
[ ] compatibility verified where required
```

### Security
```text
[ ] AppSec gate complete for R2/R3 security changes
[ ] no unresolved BLOCKER/HIGH security findings without formal exception
[ ] secrets/config reviewed
```

### Migration
```text
[ ] backup/precondition defined
[ ] migration tested on realistic old state
[ ] verification defined
[ ] irreversible point known
```

### Operations
```text
[ ] config/secrets ready
[ ] health signal defined
[ ] rollback trigger defined
[ ] rollback steps executable
[ ] monitoring/logging sufficient
```

---

## Rollout

Prefer risk-proportional rollout:
- atomic deploy for low risk,
- canary/staged for higher risk,
- feature flag only when it provides real rollback/isolation value.

A feature flag is not free complexity.

---

## Rollback Trigger

Define observable triggers before release.

Example:

```text
rollback if:
- error rate exceeds threshold for sustained interval
- migration verification fails
- unauthorized data exposure observed
- critical workflow fails
```

---

## Release Verdict

```text
READY
READY WITH ACCEPTED RISK
NOT READY
BLOCKED
```

State exact evidence and accepted risk owner when applicable.
