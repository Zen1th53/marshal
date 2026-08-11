# OWNERSHIP-ROUTING.md — Decision and Review Routing Protocol

## Mission

Send a question to the role or human that owns the decision.

## Routing

```text
architecture invariant / public contract
→ Architect

implementation detail
→ Developer

acceptance / regression verdict
→ QA

security boundary / finding
→ AppSec

risk acceptance / destructive business decision
→ authorized human/owner
```

Use `memory/OWNERSHIP.md` and repository-native CODEOWNERS when present.

## Unknown Owner

If ownership is genuinely unknown:

```text
do not self-approve
→ identify decision class
→ use TEAM authority
→ escalate to user/owner if still unresolved
```

## Review Explosion

Do not add reviewers merely because they exist.

A reviewer should have:
- authority,
- relevant expertise,
- or affected ownership.

Extra reviewers without a reason add latency, not safety.
