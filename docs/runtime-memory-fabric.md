# Runtime memory fabric implementation report

## Starting state

- Starting commit: `5bfbc44f4f50687f7c7c03ad0c3990a92bcf57ba`
- Starting branch: `main`
- Starting worktree: clean
- Implementation branch: `feat/runtime-memory-fabric`
- Canonical database: SQLite `memory_records_v2`; lexical, vector, and graph indexes remain derived projections.

The starting repository already contained canonical memory records, scope and lifecycle types, a write firewall, CAS-backed working memory, conflict and supersession helpers, hybrid candidate retrieval packages, context armor, importer interfaces, typed handoffs, and conformance suites. The production runtime did not call the product-facing memory service during provider execution, and several public interfaces bypassed that service.

## Current-state gaps found

| Gap | Root cause | Production path | Severity |
|---|---|---|---|
| No automatic recall before provider execution | `Runtime.Run` rendered task text only | Runtime, all adapters, A2A task execution | P0 |
| No automatic outcome memory | Run evidence was stored but no canonical memory candidate was created | Runtime completion | P0 |
| Recall ignored lifecycle and repository freshness | `MemoryService.Recall` was a substring filter over all project rows | Runtime service, future callers | P0 |
| No retrieval receipt or byte/record budget | Recall returned IDs and titles only | Runtime and debugging | P0 |
| CLI and MCP bypassed the facade | Both read/wrote `memory_records_v2` directly | CLI, MCP | P0 |
| Web production memory used fixture state | Memory handlers used a process-global demo store even with a live runtime | Web | P0 |
| Web mutations reported synthetic success | Mutation handlers changed only fixture rows and synthesized audit/signature IDs | Web | P0 |
| Existing hybrid/vector/graph packages remain disconnected from the canonical runtime facade | Components were tested independently but not assembled in `MemoryService` | Runtime recall quality | P1 |

## Implemented changes

| Change | Files | Why | Security implications | Compatibility implications |
|---|---|---|---|---|
| Canonical service and runtime lifecycle wiring | `internal/app/memory_runtime.go`, `internal/app/runtime.go` | Make recall and evidence-bound capture automatic | Scope/ACL/lifecycle/freshness gates run before ranking; capture remains candidate-only | Existing adapters receive provider-neutral trusted context without format-specific durable state |
| Public interface convergence | `internal/cli/memory_cli.go`, `internal/mcp/server.go` | Remove direct/synthetic recall and write paths | MCP uses the authenticated principal ID; empty scope requests fail closed to project/caller scope | Existing command/tool names and response basics remain intact; recall responses gain context and receipts |
| Canonical Web memory paths | `internal/webcontrol/memory.go`, `memory_detail.go`, `memory_mutations.go`, `retrieval_trace.go`, `server.go` | Replace production fixture state with SQLite truth | Authenticated actor filtering, direct-ID scope checks, CAS mutations, and no synthetic live success | Fixture behavior remains only for explicit nil-runtime tests/demo use |
| Cross-provider, restart, ACL, staleness, secret, and Web tests | `internal/integration/runtime_memory_fabric_test.go`, `internal/app/handoff_runtime_test.go`, `internal/webcontrol/memory_canonical_test.go` | Prove the production path with real SQLite and repository state | Covers private/task scope non-disclosure and rejected credential persistence | No schema change; tests exercise existing migration/bootstrap paths |
| Distribution and operator documentation | `distribution/PACK-MANIFEST.json`, `docs/runtime-memory-fabric.md`, `todo.md` | Keep release integrity and implementation claims synchronized | Manifest includes the new tracked tests/report/backlog | Manifest version and historical generated date remain unchanged |

### Canonical runtime recall

`Runtime` owns one per-runtime `MemoryService`. Before an adapter starts, `Runtime.Run` builds a query from the canonical task and repository execution state and requests a bounded context pack.

Recall performs these operations in order:

1. canonical project query;
2. store-level private-scope filtering;
3. scope and ACL hard gates;
4. lifecycle, expiry, and repository-HEAD freshness gates;
5. exact and lexical relevance scoring;
6. authority weighting;
7. deterministic ordering;
8. record-count and byte-budget allocation;
9. HTML/XML escaping and data-only context rendering.

The generated `<marshal_memory_context>` explicitly labels recalled content as historical data, not instructions. Adapter-specific prompt builders receive the same provider-neutral context through their existing `TrustedContext` field.

### Retrieval receipts

Every service recall returns a machine-readable receipt with:

- query and repository state;
- record and byte budgets;
- consumed bytes;
- included/excluded decision per visible candidate;
- matched retrieval tracks;
- lifecycle and authority;
- stale and budget-exclusion reasons.

Records denied by the canonical store's private-scope gate do not appear in the receipt, preventing identifier leakage.

### Automatic completion and failure capture

Every completed run whose deterministic evidence was persisted creates an evidence-linked task-scoped candidate:

- success produces an episodic candidate;
- failure, timeout, cancellation, or block produces a failure candidate;
- the body contains deterministic run metadata only;
- raw transcript and hidden reasoning are not stored;
- provider, agent, session, run, branch, base/result commits, and evidence IDs are preserved;
- lifecycle is always `candidate` and authority is always `agent`.

Promotion remains a separate privileged operation.

### Interface convergence

- CLI recall, remember, and promote route through `Runtime.Memory()`.
- MCP recall and remember route through `Runtime.Memory()`.
- A2A task execution already routes through `Runtime.Run`, so it receives automatic recall and completion capture without a parallel A2A memory store.
- A live Web server reads search, direct-ID, detail, and retrieval explanation from canonical state. Fixture data remains restricted to explicit `runtime=nil` dev/test mode.
- Live Web promote, supersede, and tombstone operations use canonical CAS mutations. They no longer synthesize successful production writes or signatures.

## Runtime architecture after changes

```text
CLI / MCP / A2A / Web / Runtime
                |
                v
       Canonical MemoryService
                |
                v
       SQLite memory_records_v2
                |
        scope + ACL hard gate
                |
      lifecycle + expiry + HEAD
                |
        exact/lexical ranking
                |
         context byte budget
                |
                v
      <marshal_memory_context>
                |
                v
        provider-neutral adapter
                |
                v
       evidence + run outcome
                |
                v
   task-scoped candidate memory
```

Derived indexes remain non-authoritative and disposable. The runtime implementation in this tranche does not require a cloud embedding service.

## Multi-agent demonstration

`TestRuntimeMemoryFabricCrossProviderRestartAndStaleness` uses a real temporary Git repository and MARSHAL SQLite database:

```text
Codex-labelled Agent A captures an evidence-linked outcome
  -> Gemini-labelled Agent B recalls it from canonical memory
  -> Runtime closes and reopens
  -> Ollama-labelled Agent C recalls the same memory ID
  -> A newer repository HEAD excludes it as stale with a receipt
```

No provider transcript or in-memory memory mock is used in this demonstration.

## Handoff demonstration

`TestA06RuntimeSubmitsProviderNeutralTypedHandoff` uses the real runtime and SQLite store. A developer-role agent submits a bounded evidence-linked handoff, the runtime closes, a new runtime opens the same repository, and a different QA-role agent consumes the persisted handoff. The test verifies sender provenance, accepted/consumed lifecycle, idempotent submission, and restart survival without transcript replay.

A2A task execution also uses `Runtime.Run`; therefore a provider change receives canonical task memory through the same automatic recall path. This tranche did not duplicate handoff state into a second memory database.

## Security verification

- Store-level operator-private filtering is verified to omit both content and memory IDs from another principal's receipt.
- The private record remains recallable by its owning principal.
- Task-start recall rejects tombstoned, rejected, superseded, expired, and repository-stale records before context compilation.
- Context content is escaped and wrapped as data.
- Completion capture stores deterministic metadata and evidence references, not raw provider output.
- Automatic completion capture is tested with a credential marker; the canonical firewall rejects it and SQLite remains empty for that memory ID.
- Web direct-ID reads repeat scope and ACL checks and hide inactive records.

## External design review

Concepts were reviewed from the following projects without copying implementation:

- MegaMemory (MIT): concept graphs and source-verified branch conflict resolution.
- cass-memory (MIT with an additional OpenAI/Anthropic rider in its current release): cross-agent procedural learning and failure lessons.
- deja-vu: local session-history indexing and compact cross-harness recall.
- Agent Memory Control Plane (MIT): candidate-first writes, source precedence, conflict receipts, and explainable retrieval.
- Agent-Memory-OS (Apache-2.0): hard ACL before ranking, disposable candidate indexes, and token-budgeted context packs.

MARSHAL retains its own types, governance, SQLite schema, and security boundaries; no external source code or dependency was added.

## Tests

Executed during implementation:

```text
go test -count=1 ./internal/app ./internal/integration
go test -count=1 ./internal/webcontrol ./internal/integration
go test -count=1 ./internal/mcp ./internal/cli ./internal/webcontrol ./internal/app ./internal/integration
go test -count=1 ./...
go vet ./...
go test -race -count=1 ./...

cd web
npm ci
npm run typecheck
npm run lint
npm run test:run
npm run build
cd ..

python3 conformance/runner.py validate-pack
python3 -m unittest discover -s conformance/tests -v
python3 -m unittest discover -s tools/tests -v
python3 -m unittest discover -s tools/tests_v6 -v
python3 tools/release_verify.py . distribution/PACK-MANIFEST.json
git diff --check
```

All commands above passed. The Web lint command reported one existing warning in `web/src/api/errors.ts:31` and no errors. Frontend tests passed 51 files and 116 tests. `npm ci` reported zero vulnerabilities. The manifest generator was run twice with the same resulting SHA-256 before verification, proving deterministic output for tracked files.

## Performance

No new 10k/100k runtime-memory benchmark was executed for this tranche, so no latency, throughput, memory-growth, Recall@K, NDCG, or task-uplift values are claimed. Existing repository benchmark and conformance tests passed as part of `go test ./...`; they are not presented as measurements of this new canonical runtime path.

## Remaining limitations

- The runtime facade currently uses deterministic exact/lexical ranking. Existing BM25/vector/graph candidate providers are not yet assembled into this production facade.
- Retrieval receipts are returned to callers but are not yet persisted as a dedicated canonical audit entity.
- Automatic capture records a deterministic episode/failure candidate; richer procedure/finding extraction and conflict reconciliation still require governance integration.
- Existing typed handoffs are canonical runtime objects, but the CLI/Web do not yet expose every handoff operation.
- Session importer interfaces exist, but automatic Codex/OpenCode/Gemini/Claude history discovery is not enabled.
- Outcome utility feedback and recall-use attribution are not yet connected to runtime success metrics.
- Federation remains outside the Community runtime; no network sync was introduced.
- Scale benchmarks for this change have not yet been executed. No new performance claim is made.
