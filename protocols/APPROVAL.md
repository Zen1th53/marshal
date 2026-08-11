# APPROVAL.md — Dangerous Operation and Human Approval Protocol

## Mission

Separate:

```text
can perform
```

from:

```text
is authorized to perform
```

---

## 1. Approval Gate

Before a dangerous operation:

```text
identify operation
→ identify scope
→ identify reversibility
→ identify blast radius
→ identify required authority
→ request approval
→ bind approval to commit/scope
→ perform
→ verify
→ consume/close approval
```

---

## 2. Never Infer Approval

Do not infer authorization from:

- repository write access,
- CI credentials,
- shell privileges,
- prior approval for a different commit,
- ability to reach production,
- user saying "continue" when the specific destructive operation was not disclosed.

---

## 3. Approval Request

State:

```text
operation
why required
exact target
irreversible effects
rollback
security/data impact
commit/artifact
verification after execution
```

---

## 4. Reapproval

Require new approval if:

- commit materially changes,
- target environment changes,
- blast radius grows,
- destructive scope changes,
- new HIGH/BLOCKER risk appears,
- previous approval expires/revokes.

---

## 5. Emergency Incident

Incident policy may permit emergency containment under pre-authorized rules.

Even then:
- preserve evidence,
- record action,
- record authority source,
- review afterward.

---

## 6. Risk Acceptance

AppSec identifies risk.

The authorized owner accepts/rejects it.

AppSec and Orchestrator do not accept organizational risk on the owner's behalf.

---

## 7. Completion

A dangerous operation is complete only when:

```text
[ ] approval valid
[ ] executed scope matches approval
[ ] result verified
[ ] approval consumed/recorded
[ ] unexpected impact documented
```
