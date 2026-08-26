# Runtime memory fabric follow-up

## Phase 2 status (2026-08-24)

Completed and validated on `feat/memory-fabric-phase2`:

- [x] Versioned 21-record/12-query golden retrieval corpus with Recall@K,
  Precision@K, MRR, NDCG, forbidden exposure, context cost, and first-useful
  timing.
- [x] ACL-first bounded candidate lookup, bounded canonical reload, bounded
  persisted receipts, and cold-projection canonical fallback.
- [x] Governed procedure, anti-pattern, and verified-fact consolidation
  candidates with provenance, scope preservation, conflict semantics, and
  completion-triggered bounded scheduling.
- [x] Durable bounded task-change cursor with monotonic revisions, refresh,
  revocation, private-event isolation, cursor expiry, restart persistence, and
  same-slot CAS conflict preservation.
- [x] Canonical/worktree HEAD and worktree identity freshness semantics.
- [x] Verified Gemini CLI JSONL importer adapter.
- [x] Opt-in real parallel provider harness; two authenticated Codex processes
  were proven concurrently active and exchanged bidirectional task findings.
- [x] 2/5/10/20/50-agent shared-write and refresh measurements with zero missed
  updates and zero duplicate deliveries in the measured run.
- [x] 100k canonical record scheduled benchmark and bounded outbox/receipt
  persistence fixes.
- [x] Truthful Web doctor probes; unavailable probes report UNKNOWN/DEGRADED
  instead of synthetic READY/0ms.

Remaining work, without overstating completion:

- [ ] Run authenticated cross-provider E2E when at least two different provider
  CLIs are available. Claude was present but logged out; Gemini/OpenCode were
  absent; Ollama had no running model service.
- [ ] Add bounded automatic session discovery/checkpointing and a verified
  OpenCode SQLite importer.
- [ ] Run 250k+ scale, long soak, and statistically powered real-provider
  memory/no-memory uplift studies.
- [ ] Reduce the 100k lexical projection's heap footprint and add cold/warm
  startup, RSS, CPU and derived-index on-disk measurements.
- [ ] Add canonical historical quality-series aggregation for the Web dashboard.

Exact Phase 2 evidence and `NOT_RUN` items are in
`memory-fabric-phase2-validation.md`.

The canonical runtime integration tranche is complete. The work below remains before the full memory-fabric mega-task can be declared complete. Items are ordered by dependency and must be delivered as reviewable stages with green tests after every stage.

## Delivery rules

- SQLite `memory_records_v2` remains authoritative. Lexical, vector, graph, and cache state must be disposable and rebuildable.
- Repository evidence, current policy, and operator authority always outrank stored memory.
- Scope and ACL checks must happen before content scoring or disclosure. Unauthorized IDs must not appear in receipts.
- Provider output can create candidates only. Promotion requires existing authority, evidence, and governance paths.
- Every write path must pass the deterministic secret firewall.
- Do not add provider-specific fields to canonical records or persist chain-of-thought.
- No schema change without a forward migration, fresh-install test, upgrade test, backup/restore verification, and manifest synchronization.

## M10 — Progressive derived-index retrieval

### Goal

Connect the existing lexical/BM25, vector, and graph components to `MemoryService` without making them authoritative.

### Implementation

- [x] Define a narrow candidate-provider interface returning memory IDs, retrieval track, score, and degraded-channel status.
- [x] Rebuild or incrementally populate lexical and graph projections from canonical records; derived searches accept the pre-authorized ID set.
- [x] Keep embeddings optional and local-first; recall works without an embedding provider and does not issue empty-vector searches.
- [x] Merge and deduplicate candidate IDs, then use the authorized canonical SQLite rows for ranking.
- [x] Apply ACL, scope, lifecycle, tombstone, expiry, conflict, and freshness gates before content scoring.
- [x] Add bounded per-track candidate limits, timeouts, graph depth, and graceful degradation.
- [x] Invalidate or rebuild projections after service, CLI, and Web mutations and at runtime startup.

### Acceptance criteria

- Deleting all derived indexes and rebuilding produces the same eligible canonical result set.
- A tombstoned record never returns through lexical, vector, graph, direct-ID, cache, or compiled context paths.
- A vector or graph failure degrades recall without bypassing authorization or breaking lexical recall.
- No cloud service is required for the default Community runtime.

### Required tests

- [x] Cross-scope exact, lexical, graph-neighbor, and cache leakage tests; vector remains disabled unless a real provider is configured and uses the same authorized-ID contract.
- [x] Tombstone followed by complete index rebuild.
- [x] Derived-index corruption followed by recovery from SQLite.
- [x] Degraded candidate-provider fallback with deterministic canonical recall.

## M11 — Persisted retrieval receipts and explanation

### Goal

Make automatic recall auditable across runtime restarts without leaking unauthorized records.

### Implementation

- [x] Add an authorized SQLite receipt repository with a forward migration.
- [x] Record query digest (never raw prompt), caller, project, allowed scopes, HEAD/branch, tracks, authority, freshness decision, budget usage, and visible IDs.
- [x] Record aggregate denial counts where individual denied IDs would leak scope membership.
- [x] Link receipts to run, task, provider, evidence, and resulting outcome.
- [x] Expose caller-bound inspect through `MemoryService.GetReceipt` and canonical recall explanation through production Web.
- [x] Define receipt retention and tombstone behavior: memory tombstones preserve caller-bound audit receipts; policy-admin retention pruning is explicit.

### Acceptance criteria

- Operators can answer why an authorized record was included or excluded after restart.
- Callers cannot infer private or foreign task memory IDs from receipts.
- Receipt persistence failure has an explicit fail-closed or degraded contract and never silently reports success.

## M12 — Governed extraction, failure learning, and conflicts

### Goal

Turn deterministic run evidence into useful candidate findings, procedures, constraints, and failure lessons without allowing models to manufacture trusted truth.

### Implementation

- [x] Add deterministic extraction inputs from persisted run evidence, exit status, test/file metadata, error signatures, commits, and verified findings.
- [x] Keep raw provider text and imported transcript claims low-authority and candidate-only.
- [x] Represent failed approach, reason, environment, evidence, and retry condition explicitly.
- [x] Detect exact duplicates in the same authorized scope before insertion.
- [x] Represent semantic disagreements explicitly and retain the competing candidate.
- [x] Require provenance/evidence and authority gates before promotion; conflicted/inactive records cannot be promoted.
- [ ] Add consolidation proposals for repeated verified facts and repeated failures without erasing provenance.

### Acceptance criteria

- A malicious provider cannot self-promote, claim operator approval, alter policy, or grant capabilities.
- A failure tied to one environment is not converted into a global blacklist.
- Concurrent contradictory findings remain `conflicted` until an authorized evidence-based resolution.
- Successful and failed runs both produce useful, secret-free candidates when qualifying evidence exists.

## M13 — Repository reconciliation and freshness

### Goal

Replace simple HEAD mismatch handling with incremental, explainable repository-aware reconciliation.

### Implementation

- [x] Persist or reuse repository identity, branch, commit, file references, content hashes, symbols, tests, and `last_verified_at` provenance.
- [x] Implement `fresh`, `possibly_stale`, `stale`, `superseded`, `conflicted`, and `unverifiable` classifications.
- [x] Detect deleted/renamed files, changed content hashes, missing symbols, newer authoritative decisions, and invalidated tests.
- [x] Reconcile incrementally from caller-supplied changed paths/hashes/symbol/test deltas instead of rescanning the full repository on every recall.
- [x] Filter stale/conflicted repository-linked records before authority and utility ranking.

### Acceptance criteria

- A code-linked record loses recall authority when its supporting file/symbol changes.
- Unrelated repository changes do not invalidate unaffected project knowledge.
- Receipts state the exact freshness signal and decision.

## M14 — Task blackboard and concurrent semantic writes

### Goal

Complete task-scoped multi-agent coordination using existing scope, CAS, conflict, and evidence primitives.

### Implementation

- [x] Store task working slots as canonical SQLite `MemoryKindWorking` rows instead of process-local state.
- [x] Preserve agent/provider/session/run provenance for task-slot proposals and competing CAS records.
- [x] Require expected revision for mutable task-working items.
- [x] On CAS failure, retain the competing proposal as a conflicted canonical row instead of last-writer-wins.
- [x] Add authorized, revocable task-scope grants for MCP and A2A clients; do not infer access from project membership alone.

### Acceptance criteria

- Two agents reading revision 14 cannot silently overwrite each other; the second write conflicts.
- Agent B can consume Agent A's verified task finding without receiving hidden state or the full transcript.
- Agents outside the task cannot discover task item content or IDs.

## M15 — Provider-neutral handoff surfaces

### Goal

Extend the existing durable typed handoff to production workflows that need it.

### Implementation

- [x] Compile bounded task definition, typed working slots, governed memory context, files, HEAD/branch/diff refs, evidence, and memory IDs through existing types.
- [x] Bind handoff to branch, HEAD, task, authenticated sender, and intended recipient role.
- [x] Reject secrets before typed handoff submission.
- [x] Expose authenticated MCP and A2A create/consume workflows; CLI/Web are intentionally not duplicated without a production consumer.
- [x] Route compiled handoffs through the existing durable authenticated `protocol.Service` rather than a second store.

### Acceptance criteria

- Codex-labelled sender to Gemini/Ollama-labelled receiver survives runtime restart without transcript replay.
- Provenance and repository freshness remain inspectable.
- Authority, capabilities, secrets, and chain-of-thought cannot be transferred in claims.

## M16 — Retroactive session importer foundation

### Goal

Safely ingest locally available agent histories through replaceable importer adapters.

### Implementation

- [ ] Automatic filesystem discovery and checkpointing remain optional follow-up; parse/redact/normalize are provider-neutral.
- [x] Add adapters for locally verified Codex JSONL and Claude JSONL formats; unsupported OpenCode/Gemini formats fail closed until verified fixtures exist.
- [ ] Capture timestamps, provider, repository, branch, commit, files, commands, exit codes, tests, edits, and tool evidence when present.
- [x] Do not persist raw provider histories; produce filtered structured episodes as agent-authority candidates.
- [x] Make imports idempotent across runtime restarts with deterministic IDs and canonical digest checks.

### Acceptance criteria

- Re-importing the same session produces no duplicate canonical memories.
- Credential fixtures in prompt, tool output, error output, or environment never reach durable memory.
- Transcript claims do not become verified facts without independent evidence and promotion.

## M17 — Outcome utility and lifecycle consistency

### Goal

Use downstream outcomes to improve ranking while keeping truth and access controls authoritative.

### Implementation

- [x] Track retrieved, included, used, helpful, ignored, contradicted, superseded, verification-contributing, and failed signals.
- [x] Bound utility counters and idempotency-event history; repeated event IDs cannot inflate rank.
- [x] Enforce lifecycle behavior in canonical Runtime, Web, MCP, A2A, derived-index, cache, promotion, handoff, and utility paths.
- [x] Invalidate/reindex derived state after runtime, CLI, and production Web lifecycle mutations.

### Acceptance criteria

- Utility cannot resurrect tombstones, bypass ACL, override repository freshness, or convert popularity into truth.
- Repeated successful evidence can raise ranking but not authority without governance.

## M18 — Federation boundary

### Goal

Define a future-safe contract without enabling an insecure distributed system in Community.

### Implementation

- [ ] Specify stable IDs, append-only mutation envelopes, provenance, scope restrictions, tombstones, revocation, and conflicts.
- [ ] Require receiver-side authorization and prohibit remote visibility widening.
- [x] Keep network federation disabled; reject claimed signatures because no peer verifier/replay protection is configured.

### Acceptance criteria

- Local operation remains complete without any network service.
- A remote peer cannot grant itself access, authority, or broader scope.

## M19 — Multi-agent evaluation and adversarial suite

### Required trajectory

- [ ] T1 Codex learns an architecture decision.
- [ ] T2 Gemini receives only the relevant governed recall.
- [ ] T3 local Ollama records an evidence-bound failure.
- [ ] T4 OpenCode avoids the failed approach.
- [ ] T5 repository code changes and the old record becomes stale.
- [ ] T6 another agent proposes supersession.
- [ ] T7 concurrent agents create conflicting findings.
- [ ] T8 evidence and authority resolve the conflict.
- [x] T9 a provider-labelled sender creates a typed handoff consumed by another provider-labelled principal without transcript replay.
- [x] T10 runtime restart preserves canonical task slots, utility metadata, memory records, receipts, and typed handoffs.

### Required adversarial coverage

- [x] Cross-scope leakage through exact ID, lexical, graph, cache, Web, MCP, A2A, and handoff; optional vector providers are constrained to authorized IDs.
- [x] Memory poisoning and forged operator/policy claims.
- [x] Stored prompt injection and delimiter breakout attempts.
- [x] Stale repository facts and current-source precedence.
- [x] Tombstone resurrection after index rebuild and runtime restart.
- [x] Concurrent CAS and semantic conflict handling.
- [x] Credentials seeded through runtime/provider text, tool-style output, session import, private slots, handoff, and errors.

## M20 — Performance and release evidence

### Measurements

- [x] Benchmark canonical paths with raw commands recorded in `memory-fabric-validation.md`: ingestion 264.395 µs/op; 10k rebuild 259.035 ms; 10k recall 315.281 ms at 3 iterations.
- [x] Benchmark canonical SQLite-backed working-memory CAS: 600.439 µs/op at 3 iterations.
- [x] Measure 20 concurrent recalls, repeated recall, bounded cache behavior, restart, and index rebuild.
- [ ] Record recall precision, Recall@K, NDCG, stale-memory retrieval, false recall, cross-agent transfer, repeated-failure reduction, context bytes/tokens, and time-to-useful-context.
- [x] Record exact CPU (Intel Core Ultra 7 255H), OS (Linux 7.0.11-arch1-1 amd64), Go version (go1.26.4-X:nodwarf5), SQLite schema v71.

### Release gates

- [x] `go test ./...` — PASS (all packages)
- [x] `go vet ./...` — PASS (clean)
- [x] `go test -race -count=1 ./internal/app ./internal/store ./internal/memory/... ./internal/integration` — PASS
- [x] Memory conformance and adversarial suites — PASS
- [x] Web `npm test` (51 test files, 116 tests) and `npm run build` — PASS
- [x] Fresh-install, migration, backup/restore, and derived-index rebuild verification — PASS
- [x] Deterministic `distribution/PACK-MANIFEST.json` regeneration and verification — PASS
- [x] Final diff review proving no unrelated changes, secrets, generated garbage, or fake benchmark claims — PASS

No benchmark value may be published unless the exact measurement command and raw result were produced successfully.
