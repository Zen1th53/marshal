# MEMORY-BACKEND.md — Backend Selection and Integration Protocol

## Mission

Prevent a useful memory concept from turning into unnecessary infrastructure.

---

## 1. Start File-First

Default:

```text
Markdown + Git
```

until there is evidence that it fails a real requirement.

---

## 2. Add SQLite When

At least one is true:

- concurrent task claims require atomicity,
- structured querying is frequent,
- Markdown write contention becomes real,
- audit/event history is cumbersome,
- agent clients need one local API.

---

## 3. Add Semantic Index When

Exact/lexical retrieval is insufficient for real queries.

Before adding TurboVec/vector indexing, demonstrate queries such as:

```text
find prior investigations conceptually similar to this failure
find lessons about visibility bugs without exact wording
```

Do not vectorize everything by default.

---

## 4. Add Graph Memory When

Relationship traversal is a real need.

Examples:

```text
which decisions affect this component?
which findings reference tests touching this API?
what task introduced the rule this file implements?
```

If normal relational queries answer it cleanly, graph infrastructure may not be needed.

---

## 5. Add Historical Session Adapter When

Agents repeatedly need:

- prior debugging path,
- previous commands,
- old touched files,
- last session context not promoted into canonical memory.

Historical recall must remain secondary to current evidence.

---

## 6. Backend Review

Architect reviews:
- ownership,
- schema,
- API,
- failure model,
- migration.

AppSec reviews:
- secrets,
- project isolation,
- network exposure,
- auth/authz,
- supply chain,
- external providers.

QA verifies:
- state consistency,
- conflict behavior,
- resume,
- failure/recovery.

---

## 7. Backend Acceptance

No backend may be adopted without:

```text
requirement
measured/current limitation
chosen capability
operational cost
failure behavior
security impact
migration/removal plan
verification
```

---

## 8. Anti-Patterns

Reject:

```text
vector database because memory sounds like embeddings
graph database because agents are complex
cloud service because local file is not fashionable
MCP gateway before there are multiple clients
full framework adoption for one primitive
```

The backend must pay rent.
