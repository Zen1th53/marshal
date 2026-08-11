# DEBUGGING.md — Root-Cause Debugging Protocol

## Rule

No random patching.

Use:

```text
OBSERVE
→ REPRODUCE
→ LOCALIZE
→ HYPOTHESIZE
→ TEST
→ ROOT CAUSE
→ FIX
→ REGRESSION
→ VERIFY
```

---

## 1. Observe

Capture:
- exact error,
- inputs,
- environment,
- version,
- timestamps/order,
- logs/trace,
- expected vs actual.

Do not paraphrase away useful detail.

---

## 2. Reproduce

Reduce to the smallest deterministic reproducer.

If intermittent:
- identify frequency,
- capture correlation data,
- remove unrelated variables.

Do not add sleeps as a first response to races.

---

## 3. Localize

Narrow:
- layer,
- component,
- function,
- data transition,
- first incorrect state.

Find where data first becomes wrong, not only where it crashes.

---

## 4. Hypothesize

Write one falsifiable hypothesis.

Bad:

```text
Maybe caching is weird.
```

Good:

```text
The node list is stale because cache invalidation runs on content publish but not on node reorder.
```

---

## 5. Test Hypothesis

Change observation first when possible:
- targeted log,
- breakpoint,
- query,
- unit probe,
- trace.

Do not modify production behavior to test five hypotheses at once.

---

## 6. Root Cause

A root cause explains:
- why the failure occurs,
- why now/under these inputs,
- why existing tests missed it.

If you cannot explain those, keep investigating.

---

## 7. Fix

Patch the cause with the smallest coherent change.

Avoid:
- catch-and-ignore,
- retry forever,
- duplicate validation in random places,
- blocking one exact bad value.

---

## 8. Regression Test

Create evidence that:
- old failure condition is covered,
- new behavior is correct,
- relevant valid behavior still passes.

For strong regression proof when feasible:
1. test passes with fix,
2. revert/disable fix,
3. test fails,
4. restore fix,
5. test passes.

---

## 9. Stop Conditions

Stop and escalate if:
- evidence points to architecture contradiction,
- data corruption is possible,
- security boundary is involved,
- environment is not reproducible enough for a safe patch.

---

## Debug Report

```markdown
## Symptom
## Environment
## Reproducer
## First Incorrect State
## Root Cause
## Fix
## Regression Test
## Verification
## Remaining Uncertainty
```
