# REVIEW.md — Engineering Review Protocol

## Purpose

Review the change, not the author's intent.

---

## Review Order

```text
1. requirement
2. architecture/invariants
3. behavior
4. failure paths
5. security
6. tests
7. maintainability
8. diff hygiene
```

Do not start with naming nits while correctness is unresolved.

---

## Review Questions

### Requirement
- Does the change solve the requested problem?
- Did scope expand?

### Correctness
- Are invariants preserved?
- What happens on invalid state?
- Any race/duplicate behavior?

### Interfaces
- Was a public contract changed?
- Is compatibility explicit?

### Error handling
- Are failures visible?
- Any silent fallback?

### Security
- New input/route/write/file/network/process/dependency?
- Auth/authz boundary changed?
- Unsafe rendering?

### Tests
- Does a test prove the behavior?
- Negative path?
- Regression?
- Are mocks hiding the target behavior?

### Diff
- Unrelated change?
- Debug code?
- Lockfile churn?
- Generated junk?

---

## Comment Format

```text
SEVERITY — short title

Fact:
<what is wrong>

Impact:
<why it matters>

Evidence:
<code path/test/reproduction>

Fix property:
<what must become true>
```

Prefer fix properties over writing the patch for the author.

---

## Severity

- BLOCKER
- HIGH
- MEDIUM
- LOW
- NIT

NIT is preference. Never disguise preference as correctness.

---

## Reviewer Discipline

Do not:
- demand a rewrite when a local fix suffices,
- enforce personal style over repository style,
- suggest speculative abstraction,
- approve from summary without reading diff,
- accept “tests pass” without scope/evidence.

If feedback is wrong after evidence, withdraw it cleanly.
