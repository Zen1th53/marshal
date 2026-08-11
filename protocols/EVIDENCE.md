# EVIDENCE.md — Verification Evidence Protocol

## Rule

A claim is only as strong as the evidence that directly proves it.

---

## Evidence Hierarchy

Prefer, roughly:

```text
reproducible automated test
runtime/integration observation
build/static-analysis output
database/query invariant check
diff/config inspection
reasoned design proof
assumption
```

The right evidence depends on the claim.

A linter cannot prove runtime correctness.
A unit test cannot prove deployment routing.
A screenshot cannot prove authorization.

---

## Evidence Record

For every material claim record:

```text
Claim:
Method:
Scope:
Result:
Limit:
```

Example:

```text
Claim:
Hidden nodes are excluded from public tree response.

Method:
pytest tests/api/test_public_tree.py::test_hidden_nodes_are_excluded -q

Scope:
public tree serialization path.

Result:
1 passed.

Limit:
does not verify CDN cache configuration.
```

---

## Freshness

Use fresh evidence from the current change state.

Do not rely on:
- yesterday's test run,
- another agent's assertion,
- “CI usually passes.”

---

## Partial Verification

Say exactly what was not verified.

Good:

```text
Unit/integration tests pass.
Production reverse-proxy routing was not verified in this environment.
```

Bad:

```text
Everything is good.
```

---

## Completion Gate

Before saying ready/done:

```text
[ ] identify each major claim
[ ] identify proving command/procedure
[ ] run it
[ ] read result
[ ] record failures/limits
[ ] only then state status
```
