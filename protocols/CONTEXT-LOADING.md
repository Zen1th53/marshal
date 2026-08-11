# CONTEXT-LOADING.md — Progressive Agent Context Protocol

## Mission

Give an agent the smallest context that is sufficient for the current decision.

More context is not automatically better.

Excess irrelevant instruction causes:

- instruction dilution,
- missed constraints,
- slower reasoning,
- token waste,
- stale-memory contamination.

---

## 1. Load Order

Always:

```text
repository policy
→ TEAM.md
→ TORVALDS.md
```

Then load:

```text
coordination file if coordinating
→ assigned role
→ task-specific protocols
→ compact current memory
→ deeper history only if needed
```

Use `AGENT-MANIFEST.yaml` as the routing map.

---

## 2. Minimum Necessary Context

Ask:

```text
What decision is the agent making right now?
Which files govern that decision?
What evidence is needed?
```

Load only that set first.

---

## 3. Resumed Work

Load:

```text
STATE
TASKS
active DECISIONS
open FINDINGS
latest relevant HANDOFF/CHECKPOINT
```

Do not inject the entire historical corpus.

---

## 4. Bugfix

Load:

```text
Developer role
DEBUGGING
EVIDENCE
relevant tests
relevant prior finding/handoff
```

Only retrieve historical sessions if current reproduction/localization benefits.

---

## 5. Security Review

Load:

```text
AppSec role
current design/implementation
THREAT MODEL template
SECURITY REVIEW template
active security decisions/findings
```

Do not load unrelated QA history.

---

## 6. Context Expansion

Expand only when the current evidence creates a concrete need.

Examples:

```text
unknown prior design rationale
→ DECISIONS / ADR

possible repeated bug
→ Deja Vu-style historical recall

relationship-heavy code path
→ graph/code-context retrieval

semantic memory query
→ TurboVec-like index
```

---

## 7. Context Freshness

Before using loaded context, classify:

```text
current
potentially stale
historical
superseded
unverified
```

Do not allow historical text to masquerade as current requirement.

---

## 8. Compression

Compress old context into:

```text
decision
finding
checkpoint
durable lesson
```

Do not repeatedly summarize summaries until provenance is lost.

---

## 9. Final Rule

Context should be:

```text
small enough to reason about
large enough to be correct
fresh enough to trust
traceable enough to verify
```
