# Runtime memory fabric follow-up

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
- [ ] Add bounded per-track candidate limits, timeouts, graph depth, and graceful degradation.
- [x] Invalidate or rebuild projections after service, CLI, and Web mutations and at runtime startup.

### Acceptance criteria

- Deleting all derived indexes and rebuilding produces the same eligible canonical result set.
- A tombstoned record never returns through lexical, vector, graph, direct-ID, cache, or compiled context paths.
- A vector or graph failure degrades recall without bypassing authorization or breaking lexical recall.
- No cloud service is required for the default Community runtime.

### Required tests

- [ ] Cross-scope exact, lexical, vector, graph-neighbor, and cache leakage tests.
- [x] Tombstone followed by complete index rebuild.
- [ ] Derived-index corruption followed by recovery from SQLite.
- [x] Degraded candidate-provider fallback with deterministic canonical recall.

## M11 — Persisted retrieval receipts and explanation

### Goal

Make automatic recall auditable across runtime restarts without leaking unauthorized records.

### Implementation

- [x] Add an authorized SQLite receipt repository with a forward migration.
- [x] Record query digest (never raw prompt), caller, project, allowed scopes, HEAD/branch, tracks, authority, freshness decision, budget usage, and visible IDs.
- [x] Record aggregate denial counts where individual denied IDs would leak scope membership.
- [ ] Link receipts to run, task, provider, evidence, and resulting outcome.
- [x] Expose caller-bound inspect through `MemoryService.GetReceipt` and canonical recall explanation through production Web.
- [ ] Define receipt retention and tombstone behavior.

### Acceptance criteria

- Operators can answer why an authorized record was included or excluded after restart.
- Callers cannot infer private or foreign task memory IDs from receipts.
- Receipt persistence failure has an explicit fail-closed or degraded contract and never silently reports success.

## M12 — Governed extraction, failure learning, and conflicts

### Goal

Turn deterministic run evidence into useful candidate findings, procedures, constraints, and failure lessons without allowing models to manufacture trusted truth.

### Implementation

- [ ] Add deterministic extraction inputs from commands, exit status, test evidence, file changes, error signatures, commits, and verified findings.
- [x] Keep raw provider text and imported transcript claims low-authority and candidate-only.
- [ ] Represent failed approach, reason, environment, evidence, and retry condition explicitly.
- [x] Detect exact duplicates in the same authorized scope before insertion.
- [x] Represent semantic disagreements explicitly and retain the competing candidate.
- [ ] Require provenance/evidence and authority gates before promotion.
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

- [ ] Persist or reuse repository identity, branch, commit, file references, content hashes, symbols, tests, and `last_verified_at` provenance.
- [x] Implement `fresh`, `possibly_stale`, `stale`, `superseded`, `conflicted`, and `unverifiable` classifications.
- [ ] Detect deleted/renamed files, changed content hashes, missing symbols, newer authoritative decisions, and invalidated tests.
- [ ] Reconcile incrementally from changed paths instead of rescanning the full repository on every recall.
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
- [ ] Preserve agent/provider/session/run provenance for every proposal.
- [x] Require expected revision for mutable task-working items.
- [x] On CAS failure, retain the competing proposal as a conflicted canonical row instead of last-writer-wins.
- [ ] Add authorized task-scope grants for MCP and A2A clients; do not infer access from project membership alone.

### Acceptance criteria

- Two agents reading revision 14 cannot silently overwrite each other; the second write conflicts.
- Agent B can consume Agent A's verified task finding without receiving hidden state or the full transcript.
- Agents outside the task cannot discover task item content or IDs.

## M15 — Provider-neutral handoff surfaces

### Goal

Extend the existing durable typed handoff to production workflows that need it.

### Implementation

- [ ] Add bounded fields for goal, status, constraints, verified findings, hypotheses, failed approaches, files, HEAD/diff refs, evidence, memory IDs, open questions, risk, and next action where existing types permit.
- [x] Bind handoff to branch, HEAD, task, authenticated sender, and intended recipient role.
- [x] Reject secrets before typed handoff submission.
- [ ] Add CLI and Web create/inspect/consume only when backed by authenticated workflows.
- [x] Route compiled handoffs through the existing durable authenticated `protocol.Service` rather than a second store.

### Acceptance criteria

- Codex-labelled sender to Gemini/Ollama-labelled receiver survives runtime restart without transcript replay.
- Provenance and repository freshness remain inspectable.
- Authority, capabilities, secrets, and chain-of-thought cannot be transferred in claims.

## M16 — Retroactive session importer foundation

### Goal

Safely ingest locally available agent histories through replaceable importer adapters.

### Implementation

- [ ] Define discovery, parse, redact, normalize, and checkpoint interfaces independent of provider formats.
- [ ] Add adapters only for formats verified from actual local Codex, OpenCode, Gemini, and Claude histories.
- [ ] Capture timestamps, provider, repository, branch, commit, files, commands, exit codes, tests, edits, and tool evidence when present.
- [ ] Store raw imports only according to explicit retention policy; produce redacted structured episodes and low-authority candidates.
- [x] Make imports idempotent across runtime restarts with deterministic IDs and canonical digest checks.

### Acceptance criteria

- Re-importing the same session produces no duplicate canonical memories.
- Credential fixtures in prompt, tool output, error output, or environment never reach durable memory.
- Transcript claims do not become verified facts without independent evidence and promotion.

## M17 — Outcome utility and lifecycle consistency

### Goal

Use downstream outcomes to improve ranking while keeping truth and access controls authoritative.

### Implementation

- [ ] Track retrieved, included, used, helpful, ignored, contradicted, superseded, and verification-contributing signals.
- [ ] Bound utility updates and protect them from one provider or repeated retrieval loops.
- [ ] Enforce candidate, working, durable, pinned, expired, stale, conflicted, superseded, and tombstoned behavior in every interface.
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

- [ ] Cross-scope leakage through exact ID, lexical, vector, graph, cache, Web, MCP, A2A, and handoff.
- [ ] Memory poisoning and forged operator/policy claims.
- [ ] Stored prompt injection and delimiter breakout attempts.
- [ ] Stale repository facts and current-source precedence.
- [ ] Tombstone resurrection after index rebuild.
- [x] Concurrent CAS and semantic conflict handling.
- [ ] Credentials seeded through prompt, provider output, tool output, session import, handoff, summary, and errors.

## M20 — Performance and release evidence

### Measurements

- [x] Benchmark canonical paths with raw commands recorded in `memory-fabric-validation.md`: ingestion 83.890 µs/op at 100 iterations; 10k rebuild 198.870 ms; 10k recall 220.726 ms.
- [x] Benchmark canonical SQLite-backed working-memory CAS: 150.352 µs/op at 100 iterations.
- [ ] Measure concurrent agents, repeated recall, bounded cache behavior, restart, and index rebuild.
- [ ] Record recall precision, Recall@K, NDCG, stale-memory retrieval, false recall, cross-agent transfer, repeated-failure reduction, context bytes/tokens, and time-to-useful-context.
- [x] Record exact CPU (Intel Core Ultra 7 255H), OS (Linux 7.0.11-arch1-1 amd64), Go version (go1.26.4-X:nodwarf5), SQLite schema v70.

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
