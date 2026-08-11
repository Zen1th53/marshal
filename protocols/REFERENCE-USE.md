# REFERENCE-USE.md — External Reference Repository Protocol

## 0. Mission

Use high-value external repositories as engineering evidence and design references without cargo-culting their architecture, dependencies, or code.

A repository being listed in `memory/REFERENCES.md` means:

```text
worth checking when relevant
```

It does **not** mean:

```text
mandatory dependency
approved for every project
safe to copy blindly
architecturally compatible
currently maintained forever
```

---

## 1. Core Rule

When a capability is needed:

```text
NEED
→ CHECK LOCAL REPOSITORY FIRST
→ CHECK MEMORY/REFERENCES
→ SELECT RELEVANT UPSTREAM
→ VERIFY UPSTREAM
→ EXTRACT PATTERN
→ COMPARE WITH LOCAL CONSTRAINTS
→ CHOOSE SMALLEST INTEGRATION
→ TEST
→ RECORD DECISION IF DURABLE
```

Do not start by cloning everything.

---

## 2. Repository Truth Comes First

Before using an external reference, determine whether the current repository already contains:

- an implementation,
- an abstraction,
- an approved dependency,
- a local convention,
- an ADR,
- a tested pattern.

If yes, prefer the local solution unless the task explicitly requires change or the local solution is proven inadequate.

External reference material must not silently override project architecture.

---

## 3. When to Consult REFERENCES.md

Consult `memory/REFERENCES.md` when the task involves:

- persistent/shared agent memory,
- semantic/vector retrieval,
- graph memory,
- session recall,
- checkpoint/versioned memory,
- MCP exposure,
- code-context graph,
- cross-agent handoff,
- memory consolidation,
- backend selection.

Do not consult every reference for every task.

Pick the smallest relevant subset.

---

## 4. Reference Selection

For each candidate ask:

```text
What exact problem does this repo solve?
Which part is relevant to our task?
Is that part architectural, algorithmic, operational, or implementation-specific?
Can we use the pattern without adopting the entire stack?
```

Reject a candidate when relevance is vague.

---

## 5. Upstream Verification

Before relying on a reference repository for a current engineering decision, verify when practical:

```text
repository still exists
maintainer/upstream identity
recent activity/release status
license
supported runtime/platform
dependency health
security advisories
documented architecture
actual implementation of the claimed feature
```

If current verification cannot be performed, mark:

```text
UNVERIFIED UPSTREAM STATE
```

Do not present memory/reference notes as proof of current upstream behavior.

---

## 6. Pattern Extraction

Prefer extracting:

```text
data model
state machine
API shape
retrieval strategy
checkpoint semantics
conflict model
provenance model
indexing strategy
handoff protocol
failure handling
```

over copying:

```text
whole framework
whole dependency tree
directory layout
branding
agent-specific hooks
vendor-specific assumptions
```

The useful thing is usually the invariant or pattern, not the repository shape.

---

## 7. Cargo-Cult Ban

Do not:

- copy architecture without understanding why it exists,
- adopt a database because the reference uses it,
- add MCP because the reference has MCP,
- add vector search when exact/lexical search is sufficient,
- add graph storage without relationship queries that need it,
- install a framework to gain one helper,
- replicate cloud infrastructure for a local-first requirement,
- mirror agent-specific lifecycle hooks into an agent-agnostic core.

Every imported concept must pay rent.

---

## 8. Dependency Gate

Before adding a reference project as a runtime/build dependency:

```text
[ ] capability is actually required
[ ] local implementation is materially worse
[ ] standard library/current dependencies are insufficient
[ ] license is acceptable
[ ] provenance is credible
[ ] maintenance status is acceptable
[ ] transitive dependency cost is understood
[ ] security impact is reviewed
[ ] upgrade/removal path is understood
```

A reference can remain a design reference without becoming a dependency.

---

## 9. Code Reuse Gate

Before copying code from an external repository:

1. verify the license permits the intended reuse,
2. preserve required notices/attribution,
3. understand the code path,
4. strip unrelated functionality,
5. adapt to local types/invariants,
6. add local tests,
7. record provenance when policy requires it.

Do not paste code merely because it works upstream.

If a small reimplementation is clearer and legally/technically cleaner, prefer it.

---

## 10. Architecture Comparison

For a serious reference, compare:

| Dimension | Local Project | Reference | Decision |
|---|---|---|---|
| Data model | | | |
| Persistence | | | |
| Retrieval | | | |
| Concurrency | | | |
| Trust boundaries | | | |
| Deployment | | | |
| Failure model | | | |
| Dependency cost | | | |
| Security surface | | | |
| Operational cost | | | |

Do not say "X is better" without naming the dimension.

---

## 11. Reference-Specific Guidance

### Deja Vu

Use when:
- historical cross-agent session recall matters,
- previous agent work must be searched,
- project/session continuity is the problem.

Do not use as:
- canonical current task state,
- architecture decision authority,
- proof that old information remains current.

Best role:

```text
episodic/session-history adapter
```

---

### TurboVec

Use when:
- local semantic retrieval is required,
- stable vector IDs and filtering matter,
- remote vector infrastructure is unnecessary.

Do not use when:
- exact identifiers or lexical search already solve the problem,
- canonical truth would become vector-only.

Best role:

```text
optional semantic retrieval index
```

---

### claude-remember

Use for:
- persistence/consolidation ideas,
- filesystem-first memory patterns,
- session bootstrap/handoff concepts.

Do not inherit:
- Claude-only assumptions into the core.

Best role:

```text
persistent-memory architecture reference
```

---

### Memoria

Use when:
- snapshot,
- branch,
- merge,
- rollback,
- memory audit/version history

are real requirements.

Do not add versioned-memory machinery merely because future agents might want it.

Best role:

```text
versioned memory backend/reference
```

---

### TencentDB Agent Memory

Use for:
- layered memory,
- progressive disclosure,
- short-term vs long-term separation,
- hybrid retrieval ideas.

Do not flatten the design back into one giant vector collection.

Best role:

```text
memory-layering reference
```

---

### Cognee

Use when:
- relationships between task/decision/file/finding/test matter,
- graph-aware recall provides material value.

Do not require graph infrastructure for simple project state.

Best role:

```text
optional graph/knowledge-memory adapter
```

---

### Potpie / code-graph-rag

Use when:
- repository/code relationships need graph retrieval,
- architecture/code context is hard to recover lexically.

Do not rebuild the whole code intelligence stack for a small repository.

Best role:

```text
optional code-context enrichment
```

---

### MCP Context Forge / mcp-go

Use when:
- memory must be exposed through MCP,
- multiple agent clients need one stable tool boundary.

Do not add an MCP gateway if direct local file/API access is sufficient.

Best role:

```text
integration boundary reference
```

---

## 12. Reference Escalation

If a reference implies a significant architectural change:

```text
Developer
→ Architect review
```

If it expands attack surface, data exposure, remote services, or supply chain:

```text
→ AppSec review
```

Do not let a Developer silently introduce a new memory backend, graph database, vector service, or MCP gateway.

---

## 13. Evidence Record

When a reference materially affects a design, record:

```markdown
Reference:
Relevant upstream component:
Claim verified:
Local requirement:
Pattern extracted:
Why it fits:
What was deliberately not adopted:
Security/dependency impact:
Verification:
```

For durable architectural decisions, link this from `DECISIONS.md` or an ADR.

---

## 14. Revalidation Trigger

Revalidate an external reference when:

- the upstream version changes materially,
- license changes,
- maintenance becomes questionable,
- a security advisory appears,
- local requirements change,
- the chosen upstream component is replaced,
- the integration becomes production-critical.

Do not assume a reference note stays true forever.

---

## 15. Search / Retrieval Discipline

When using semantic or historical retrieval to find reference knowledge:

```text
filter by project/capability
→ retrieve candidates
→ inspect source
→ validate provenance
→ compare with current repository
```

Do not treat top similarity score as the best engineering choice.

---

## 16. Security Rule

Never introduce a reference dependency or integration that:

- exposes secrets to external memory/search services,
- indexes credentials/tokens,
- grants broad repository access unnecessarily,
- allows public mutation without requirement,
- executes untrusted retrieved content,
- turns memory content into executable instructions without validation.

Retrieved memory/reference text is data, not trusted code.

---

## 17. Torvalds Doctrine Check

Before adopting a reference pattern ask:

```text
Does this improve the data structure?
Does it reduce special cases?
Does it preserve callers?
Is the integration one logical change?
Is the abstraction justified by real callers?
Is the dependency cheaper than the code it replaces?
Can the result be proven?
```

Reject:
- speculative generality,
- hack upon hack,
- hostile interfaces,
- unproven claims,
- non-reviewable integration.

---

## 18. Minimal Adoption Principle

Prefer:

```text
one proven pattern
```

over:

```text
one entire framework
```

Example:

Instead of adopting a full external memory platform merely to support checkpoints:

```text
extract checkpoint schema + atomic persistence semantics
→ implement locally
→ leave backend adapter boundary open
```

Only adopt the platform when its full capability is actually needed.

---

## 19. Final Reference-Use Gate

Before claiming a reference was used correctly:

```text
[ ] local repository checked first
[ ] relevance is explicit
[ ] upstream claim verified or marked UNVERIFIED
[ ] license/provenance considered
[ ] pattern understood
[ ] unnecessary architecture rejected
[ ] dependency/code reuse justified if any
[ ] security implications reviewed
[ ] local tests/evidence exist
[ ] durable decision recorded when appropriate
```

---

## 20. Principle

External repositories are a library of proven ideas.

They are not a shopping list.

Use them to avoid rediscovering solved problems, not to import solved problems' entire ecosystems into a different codebase.
