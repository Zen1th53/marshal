# INCIDENT.md — Production Incident Protocol

## Objective

Restore safety and service first. Preserve evidence. Fix root cause after containment.

---

## Order

```text
1. DETECT
2. CLASSIFY
3. CONTAIN
4. PRESERVE EVIDENCE
5. RESTORE SAFE SERVICE
6. IDENTIFY ROOT CAUSE
7. REMEDIATE
8. VERIFY
9. DOCUMENT
```

---

## Immediate Questions

```text
What is broken?
Who/what is affected?
Is data integrity/confidentiality at risk?
Is the issue ongoing?
What recent change may correlate?
Can we safely roll back?
What evidence will disappear if we act?
```

---

## Containment

Prefer reversible containment:
- disable affected feature,
- revoke exposed credential,
- isolate endpoint,
- rollback release,
- block unsafe input class,
- reduce privilege.

Do not destroy evidence unnecessarily.

---

## Security Incident

If compromise is suspected:
- involve AppSec immediately,
- preserve logs/artifacts,
- rotate/revoke exposed secrets,
- avoid attacker notification through careless changes where relevant,
- do not run destructive cleanup before evidence capture.

---

## Restoration

Restore known-good behavior.

Do not deploy speculative multi-change “fix packs.”

---

## Post-Incident

Produce:

```markdown
## Impact
## Timeline
## Detection
## Containment
## Root Cause
## Why Controls Failed
## Fix
## Regression Prevention
## Security Follow-up
## Operational Follow-up
```

Blameless does not mean causeless. Name failed mechanisms precisely.
