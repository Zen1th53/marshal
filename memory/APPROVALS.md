# APPROVALS.md — Human / Owner Approval Ledger

## Purpose

Record explicit authorization for operations where technical capability is not sufficient authority.

This file is not a substitute for platform access control.

---

## Operations That Commonly Require Approval

Project policy may add/remove items.

Typical examples:

- destructive data migration,
- production deployment,
- production data deletion,
- force push to shared/protected history,
- public API breaking change,
- credential/key rotation,
- secret revocation,
- irreversible storage conversion,
- large dependency/infrastructure introduction,
- public exposure of an internal/admin service,
- risk acceptance for blocking security finding.

---

## Record Format

```yaml
id: APR-000
operation: none
scope: none
requested_by: none
approved_by: none
status: requested
created_at: null
expires_at: null

repository:
  branch: null
  commit: null

conditions: []
evidence_required: []
result: null
```

Allowed status:

```text
requested
approved
denied
expired
consumed
revoked
```

---

## Approval Binding

Approval must bind to a meaningful scope.

Bad:

```text
approved to deploy
```

Good:

```text
approved to deploy commit abc123 to staging/prod under release plan REL-42
```

Do not reuse an approval after material scope changes.

---

## Current Approvals

_No approvals recorded yet._
