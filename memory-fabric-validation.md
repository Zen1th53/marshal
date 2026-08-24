# Runtime memory fabric implementation report

## 1. Starting state

- Branch: `feat/runtime-memory-fabric-completion`
- Starting HEAD: `9a6f1851950b9443621d8686ed8bccde885fcbb5`
- Tracking ref at audit start: `origin/main`
- Starting worktree: dirty; the M10-M20 implementation was uncommitted and required production-path validation.
- Existing architecture: SQLite `memory_records_v2` was already canonical, with separate lexical, vector, graph, cache, governance, writeback, protocol handoff, and Web components. Several of those components were not connected to the same production path.

## 2. Current-state gaps found

| Gap | Root cause | Production path | Severity |
| --- | --- | --- | --- |
| Derived retrieval could score before a canonical authorization set was established | Candidate channels accepted caller scopes independently | Runtime recall | Critical |
| Callers could request an arbitrary task scope | `AllowedScopeIDs` was trusted without a durable grant or active task assignment | Runtime, handoff | Critical |
| Working memory and utility state were process-local | Existing managers were wired directly instead of persisted through canonical records | Runtime, Web | High |
| Receipts persisted raw queries and were not caller-bound | Receipt schema/service lacked privacy and ownership rules | Runtime, Web explainability | High |
| Default vector retrieval used empty embeddings | A placeholder backend was treated as a real semantic track | Runtime recall | High |
| Compiled handoffs were not the durable protocol object | Compilation and typed handoff storage were separate paths | Cross-provider handoff | High |
| Production Web working-memory routes used fixture globals | Demo storage was reachable in the production handler path | Web | High |
| Secret patterns omitted common bearer, cookie, JWT, and OAuth forms | Deterministic firewall coverage was incomplete | Every canonical write/handoff | High |
| Federation import could imply trust without a verifier | Signature claims had no trust-store or replay enforcement | Portable memory boundary | High |
| Reported CAS/performance numbers measured a process-local helper | Benchmark target was not the canonical SQLite path | Release evidence | Medium |

## 3. Implemented changes

| Change | Primary files | Reason | Security / compatibility |
| --- | --- | --- | --- |
| Canonical progressive recall with authorized ID prefilter, canonical reload, freshness/lifecycle gates, bounded context, and authority-first ranking | `internal/app/memory_runtime.go`, `internal/memory/index/lexical/indexer.go` | Make SQLite authoritative across tracks | Unauthorized IDs never enter content scoring; vector remains optional |
| Durable, caller-bound retrieval receipts with query digest only | `internal/store/migrations.go`, `internal/store/memory.go`, `internal/app/memory_runtime.go` | Explain recall across restarts | Schema v70 forward migration; raw prompts are not stored |
| Governed candidate extraction, deduplication, explicit conflicts, and write-scope validation | `internal/app/memory_runtime.go` | Prevent self-promotion and forged private/project scopes | Candidate authority remains agent-level; existing promotion authority is reused |
| Repository freshness classifications and recall filtering | `internal/app/memory_runtime.go` | Ensure repository truth outranks history | Stale/conflicted/superseded records are excluded or penalized with receipt reasons |
| Canonical task blackboard, private slots, CAS, and retained competing proposals | `internal/app/memory_runtime.go`, `internal/memory/working/scratchpad.go` | Share bounded task state across agents and restarts | Explicit role binding or active task session is required; no silent last-write-wins |
| Durable provider-neutral memory handoff | `internal/app/runtime.go`, `internal/app/handoff_runtime_test.go` | Continue without transcript replay | Authenticated sender, task/repository binding, secret firewall, typed protocol storage |
| Generic idempotent session-import foundation | `internal/app/memory_runtime.go` | Normalize historical evidence without provider coupling | Imports remain candidate/low-authority and pass the store firewall |
| Canonical outcome utility metadata | `internal/app/memory_runtime.go` | Persist downstream usefulness | Utility cannot outrank authority, ACL, lifecycle, or freshness |
| Fail-closed federation boundary | `internal/memory/portable/federation_boundary.go` | Preserve a future sync boundary without insecure networking | Private/non-project export is denied; signed packs are rejected until verification exists |
| Canonical Web working memory and projection invalidation | `internal/webcontrol/working_memory.go`, `internal/webcontrol/memory*.go`, `internal/cli/memory_cli.go` | Remove independent production state | Nil-runtime fixture behavior remains test/demo-only; non-project Web reads fail closed |
| Expanded deterministic secret firewall | `internal/memory/security/firewall.go` | Block more credential classes at the canonical boundary | Store write/update firewall remains the final enforcement point |

## 4. Runtime architecture after changes

```text
Task claim / active session
        |
        v
Deterministic task/repository/provider fingerprint
        |
        v
Canonical MemoryService recall
        |
        +--> explicit scope grant / active assignment
        +--> canonical SQLite candidate rows
        +--> authorized IDs -> lexical / graph / optional vector tracks
        +--> canonical reload -> lifecycle / ACL / freshness gates
        +--> authority + relevance + bounded utility ranking
        +--> token/byte budget compiler + persisted receipt
        |
        v
Provider-neutral armored context -> provider adapter
        |
        v
Run evidence and outcome -> secret firewall -> agent candidate
        |
        v
Existing governance / promotion -> canonical SQLite memory
```

Derived indexes and caches are rebuilt at runtime startup and remain disposable.

## 5. Multi-agent demonstration

`TestRuntimeMemoryFabricCrossProviderRestartAndStaleness` and
`TestM19_MultiAgentPersistenceAndSecurityScenario` exercise real temporary
repositories and SQLite databases:

```text
provider-labelled Agent A captures a task outcome
  -> canonical SQLite candidate
  -> explicitly granted Agent B recalls it
  -> runtime closes and reopens
  -> Agent C recalls the same record
  -> changed repository HEAD excludes the stale record
```

No in-memory memory-store mock is used in this trajectory.

## 6. Handoff demonstration

`TestMemoryHandoffUsesDurableTypedServiceAcrossRestart` creates canonical task
memory, compiles a bounded reference packet, submits it through the authenticated
typed handoff service, restarts the runtime, and consumes it as another
provider-labelled principal. Memory IDs, evidence references, sender provenance,
and task working state survive; the transcript is not replayed.

## 7. Security verification

| Area | Result | Evidence |
| --- | --- | --- |
| ACL / cross-scope | PASS for implemented canonical Runtime/Web/derived paths | hard-gate, receipt non-disclosure, explicit task-grant, private-slot tests |
| Secret write and handoff rejection | PASS for deterministic fixtures | store firewall, outcome, importer, handoff, JWT/header/cookie/OAuth tests |
| Poisoning / self-promotion | PASS | candidate authority and unauthorized promotion tests |
| Prompt delimiter armor | PASS | compiled context escaping test |
| Repository staleness | PASS | deleted file, HEAD divergence, expiration tests |
| CAS and semantic conflicts | PASS | concurrent task-slot and conflicting-candidate tests |
| Tombstone rebuild | PASS | canonical tombstone followed by projection rebuild |
| Federation trust boundary | PASS | scope narrowing, downgrade, unsigned-only local import tests |

This finite fixture suite is evidence for the tested paths, not a universal
zero-leak guarantee. Full MCP/A2A task-blackboard surface parity remains open.

## 8. Performance

Environment:

```text
CPU: Intel(R) Core(TM) Ultra 7 255H
OS: Linux 7.0.11-arch1-1 x86_64 GNU/Linux
Go: go1.26.4-X:nodwarf5 linux/amd64
SQLite schema: v70
```

Raw commands and results:

```text
go test -run '^$' -bench '^BenchmarkMemoryIngestionThroughput$' -benchmem -benchtime=100x ./internal/app
BenchmarkMemoryIngestionThroughput-16  100  83890 ns/op  7247 B/op  94 allocs/op

go test -run '^$' -bench '^BenchmarkWorkingMemoryCASThroughput$' -benchmem -benchtime=100x ./internal/app
BenchmarkWorkingMemoryCASThroughput-16  100  150352 ns/op  16602 B/op  315 allocs/op

go test -run '^$' -bench '^BenchmarkDerivedIndexRebuild10kRecords$' -benchmem -benchtime=1x ./internal/app
BenchmarkDerivedIndexRebuild10kRecords-16  1  198869716 ns/op  74249040 B/op  710285 allocs/op

go test -run '^$' -bench '^BenchmarkRecallLatency10kRecords$' -benchmem -benchtime=1x ./internal/app
BenchmarkRecallLatency10kRecords-16  1  220726427 ns/op  110569864 B/op  740699 allocs/op
```

Recall@K, NDCG, 100k-record, and task-success-uplift figures were not measured.

## 9. Tests

```text
go test -count=1 ./...                                                        PASS
go vet ./...                                                                 PASS
go test -race -count=1 ./internal/app ./internal/store ./internal/memory/... ./internal/integration
                                                                             PASS
cd web && npm ci                                                             PASS (0 vulnerabilities)
cd web && npm run typecheck                                                  PASS
cd web && npm run lint                                                       PASS (one no-useless-escape warning)
cd web && npm test -- --run                                                  PASS (51 files, 116 tests)
cd web && npm run build                                                      PASS
python3 conformance/runner.py validate-pack                                  PASS
python3 -m unittest discover -s conformance/tests -v                         PASS (6 tests)
python3 -m unittest discover -s tools/tests -v                               PASS (3 tests)
python3 -m unittest discover -s tools/tests_v6 -v                            PASS (13 tests)
python3 tools/release_verify.py . distribution/PACK-MANIFEST.json            PASS
```

Fresh install, schema 69-to-70 upgrade, backup/restore, migration idempotence,
and derived-index destruction/rebuild are included in the passing Go tests and
also received targeted runs during implementation.

## 10. Remaining limitations

- No embedding provider is configured by default. Semantic vector retrieval is disabled instead of simulated with empty vectors.
- MCP/A2A task-blackboard grant-management parity remains open.
- Session import supports the verified generic JSON transcript contract, not discovery adapters for every provider history format.
- Federation has no network transport, signature trust store, replay protection, or remote mutation application.
- Recall@K, NDCG, task-success uplift, and 100k-record measurements remain open in `todo.md`.
- Web/CLI/MCP do not expose every typed handoff operation; the authenticated runtime API is canonical.

## 11. Additional findings

- The original M10-M20 checklist and benchmark claims overstated completion; unchecked work is preserved in `todo.md` rather than hidden.
- `memory_records_v2` remains canonical; legacy memory tables still exist for compatibility and are architectural debt until a governed migration/removal plan exists.
- Graph and lexical projections are in-process and rebuilt from SQLite; a configured real semantic embedding provider is still required for vector recall.
- Web lint reports one existing unnecessary-escape warning in `src/api/errors.ts`; it is non-fatal and unrelated to this change.
- Live proprietary-provider network E2E was not run because credentials were not supplied.
