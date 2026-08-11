# REFERENCES.md — Memory Architecture References

These are **reference implementations**, not mandatory dependencies.

Before adopting any reference, follow `protocols/REFERENCE-USE.md`.

The core design borrows useful patterns while remaining backend-agnostic.

---

## Deja Vu

Repository:

`https://github.com/vshulcz/deja-vu`

Use as reference for:

- cross-agent coding-session recall,
- MCP-based historical retrieval,
- project-scoped recall,
- session/handoff continuity,
- historical context without requiring one model vendor.

Role in our architecture:

```text
optional episodic/session-history adapter
```

Do not make raw historical transcripts the canonical current state.

---

## TurboVec

Repository:

`https://github.com/RyanCodrai/turbovec`

Use as reference/backend candidate for:

- local semantic retrieval,
- vector indexing,
- stable external IDs,
- filtered candidate search,
- persistence without a remote vector service.

Role:

```text
optional semantic index
```

Vector similarity is not source of truth.

Canonical records and provenance remain authoritative.

---

## claude-remember

Repository:

`https://github.com/Digital-Process-Tools/claude-remember`

Use as reference for:

- persistent context across sessions,
- memory consolidation/retention ideas,
- session bootstrap/handoff patterns,
- filesystem-first memory,
- separating core memory from agent-specific integration.

Role:

```text
architecture reference
```

Our core must remain agent-agnostic rather than Claude-specific.

---

## Memoria

Repository:

`https://github.com/matrixorigin/Memoria`

Use as reference for:

- snapshot / branch / merge / rollback,
- mutation audit trail,
- contradiction governance,
- versioned agent memory,
- semantic + full-text retrieval,
- explicit memory classes.

Role:

```text
optional future versioned-memory backend
```

Do not auto-merge contradictory engineering decisions.

---

## TencentDB Agent Memory

Repository:

`https://github.com/Tencent/TencentDB-Agent-Memory`

Use as reference for:

- layered memory rather than one flat vector pile,
- short-term vs long-term separation,
- progressive disclosure,
- local-first storage,
- hybrid lexical + semantic retrieval,
- human-debuggable memory layers,
- cross-agent portability.

Role:

```text
layering / consolidation reference
```

New agents should see compact current state first, deeper history only when needed.

---

## Cognee

Repository:

`https://github.com/topoteretes/cognee`

Use as reference/backend candidate for:

- persistent shared agent memory,
- graph + vector retrieval,
- relationship-aware context,
- dataset/scope isolation,
- traceability and cross-agent knowledge sharing.

Role:

```text
optional graph/knowledge-memory adapter
```

Do not require a graph database for V1.

---

## Potpie

Repository:

`https://github.com/potpie-ai/potpie`

Use as adjacent reference for:

- source/repository context graphs,
- linking code, architecture, and engineering context.

Role:

```text
optional code-context graph reference
```

---

## code-graph-rag

Repository:

`https://github.com/vitali87/code-graph-rag`

Use as adjacent reference for:

- Tree-sitter-derived code graph,
- relationship-aware repository retrieval.

Role:

```text
optional repository-graph enrichment reference
```

---

## MCP Context Forge

Repository:

`https://github.com/IBM/mcp-context-forge`

Use as reference for:

- MCP routing/gateway boundaries,
- exposing memory capabilities through a stable MCP surface.

Role:

```text
integration/gateway reference
```

---

## MCP Go

Repository:

`https://github.com/mark3labs/mcp-go`

Use as reference if the memory service is implemented in Go and needs MCP client/server support.

---

## Synthesis

```text
STATE / DECISIONS / FINDINGS
        ↓ canonical truth
structured memory records + provenance
        ↓
lexical index
        ↓
TurboVec semantic index (optional)
        ↓
Cognee graph context (optional)
        ↓
Deja Vu historical-session recall (optional)

Memoria
→ versioning/checkpoint/audit inspiration

TencentDB Agent Memory
→ layered/progressive disclosure inspiration

claude-remember
→ persistent session/bootstrap/consolidation inspiration
```

The important rule:

```text
retrieval system != truth
```

Retrieval systems find candidate context.
The canonical structured record plus current repository evidence determines truth.
