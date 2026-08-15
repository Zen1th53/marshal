# T43 Structured Event Stream Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement MARSHAL TERRA T43 as a durable, ordered, redacted structured event stream with resume-by-sequence subscribers and a non-authoritative in-process live bus.

**Architecture:** Add `internal/events` as the typed contract/service boundary, persist canonical events through the existing SQLite store/migration framework, and publish only after durable append. Sequence allocation/idempotency and redaction remain authoritative in persistence/service code; the live bus is lossy/rebuildable from durable history and must never block critical transitions indefinitely.

**Tech Stack:** Go, existing SQLite store/migrations, T06 evidence graph references, `context.Context`, standard library synchronization/channels, existing event registry and secret sanitization conventions.

## Global Constraints

- T43 depends on completed T06 and current canonical completion baseline.
- All commits: `Zen1th53 <extreme29@proton.me>` only.
- A01→A10 serial inside T43; independent from T29 until integration.
- Durable append always precedes live publish.
- Sensitive/raw provider output is forbidden in event payloads; store bounded references/digests instead.
- Sequence/idempotency correctness must work across multiple Store instances.
- Bus loss/backpressure may not erase durable history or deadlock canonical mutations.
- Release-pack manifest is regenerated and verified after tracked changes.

---

### Task 1: A01 Contract, Types, IDs, Errors

**Files:**
- Create: `internal/events/types.go`
- Create: `internal/events/errors.go`
- Create: `internal/events/types_test.go`
- Create: `internal/events/adversarial_test.go`

**Interfaces:**
- Produces: `Event{ID,Sequence,Type,Subject,TaskID,RunID,ResourceID,EvidenceID,At,Data,IdempotencyKey}`, `Store.Append/Since`, `Bus.Publish/Subscribe`, typed errors.

- [ ] **Step 1: RED contract test** for unknown event type and secret-bearing data.

```go
func TestEventRejectsUnknownType(t *testing.T) {
    event := Event{ID: "evt-1", Type: Type("unknown"), Subject: "system"}
    if err := event.Validate(); !errors.Is(err, ErrInvalidType) {
        t.Fatalf("Validate() error = %v", err)
    }
}
```

- [ ] **Step 2: Implement closed event vocabulary/validation** using canonical registry and stable `EVENT_TYPE_INVALID`, `EVENT_SECRET_FIELD`, `EVENT_STORE_FAILED`, `EVENT_SEQUENCE_CONFLICT` codes.
- [ ] **Step 3: Bound/copy Data** so caller mutation cannot alter validated records.
- [ ] **Step 4: Run focused/no-leak tests**.
- [ ] **Step 5: Commit** `feat(T43.A01): define structured event contracts`.

### Task 2: A02 Persistence, Sequence, Migration

**Files:**
- Modify: `internal/store/migrations.go`
- Create: `internal/store/events.go`
- Create: `internal/store/events_a02_test.go`
- Create: `internal/events/bus.go`

**Interfaces:**
- Produces: durable `Append`, `Since(sequence, limit)`, unique ordered sequence, idempotency key semantics, in-process bus fed only after commit.

- [ ] **Step 1: RED persistence test** proves two events append in strict sequence and survive reopen.
- [ ] **Step 2: Add next legal schema migration** with unique `event_id`, `sequence`, `idempotency_key` and hot sequence/reference indexes.
- [ ] **Step 3: Implement atomic sequence allocation/idempotent append** across multiple Store instances.
- [ ] **Step 4: Implement bus publish after successful durable append only**.
- [ ] **Step 5: Commit** `feat(T43.A02): persist ordered event stream`.

### Task 3: A03 State Machine / Service Logic

**Files:**
- Create: `internal/events/engine.go`
- Create: `internal/events/engine_test.go`
- Modify: `internal/events/bus.go`

**Interfaces:**
- Produces explicit lifecycle `produced -> validated -> durably_appended -> published -> consumed` and deterministic resume-by-sequence service behavior.

- [ ] **Step 1: RED illegal-transition test** proving zero durable/live side effect.
- [ ] **Step 2: Implement explicit transition validation** and immutable result objects.
- [ ] **Step 3: Implement resume/subscriber semantics** from durable sequence.
- [ ] **Step 4: Prove bus loss can be recovered via `Since`**.
- [ ] **Step 5: Commit** `feat(T43.A03): implement event stream state machine`.

### Task 4: A04 Security / Redaction Boundary

**Files:**
- Create: `internal/events/security.go`
- Create: `internal/events/security_a04_test.go`
- Modify: `internal/events/engine.go`

**Interfaces:**
- Produces: canonical redaction/validation boundary for security-sensitive structured event data.

- [ ] **Step 1: RED secret-marker test** proving raw secret/provider output cannot persist/publish.
- [ ] **Step 2: Reuse existing sanitizer/secret classification where authoritative**; do not invent parallel secret broker.
- [ ] **Step 3: Validate canonical subject/task/run/resource/evidence references** and reject malformed foreign identity where required.
- [ ] **Step 4: Attack provider/admin/trusted metadata spoofing**; event facts never become authority.
- [ ] **Step 5: Commit** `feat(T43.A04): enforce event security boundary`.

### Task 5: A05 Evidence / Provenance / Audit Integration

**Files:**
- Create: `internal/events/evidence.go`
- Create: `internal/events/evidence_a05_test.go`
- Modify existing T06 integration only through public contracts.

**Interfaces:**
- Produces bounded event references to T06 evidence and canonical audit facts; no duplicated evidence graph.

- [ ] **Step 1: RED integration test** for event carrying an evidence reference without copying evidence payload.
- [ ] **Step 2: Link event/evidence using authoritative IDs/relations**.
- [ ] **Step 3: Ensure append/audit ordering cannot create false success**.
- [ ] **Step 4: Prove prior event/evidence cannot be replayed as current authorization**.
- [ ] **Step 5: Commit** `feat(T43.A05): integrate event evidence references`.

### Task 6: A06 Runtime / CLI / Protocol Integration

**Files:**
- Modify: `internal/app/runtime.go`
- Create: `internal/app/events_runtime.go`
- Create: `internal/app/events_runtime_a06_test.go`
- Modify protocol/CLI surfaces only where T43 explicitly requires event streaming/resume.

**Interfaces:**
- Produces canonical lifecycle events for agent/task/tool/policy/file/test/verification/approval runtime boundaries.

- [ ] **Step 1: RED runtime test** for missing canonical event around one required lifecycle boundary.
- [ ] **Step 2: Wire shared runtime/event service** rather than per-provider implementations.
- [ ] **Step 3: Verify durable append occurs before live publish** and runtime success semantics do not depend on live subscriber availability unless spec requires it.
- [ ] **Step 4: Test provider-neutral fake paths and sequence resume**.
- [ ] **Step 5: Commit** `feat(T43.A06): integrate runtime event stream`.

### Task 7: A07 Adversarial / Fuzz Hardening

**Files:**
- Extend: `internal/events/adversarial_test.go`
- Create: `internal/events/fuzz_test.go`, `internal/store/events_a07_test.go` as needed.

**Interfaces:**
- Produces attack/fuzz regression corpus for secret data, malformed types/refs, sequence/idempotency confusion, bounds, subscriber misuse.

- [ ] **Step 1: Build 30+ attack/property matrix** from T43 test/fuzz/security specs.
- [ ] **Step 2: Fuzz event validation, Data serialization, idempotency identities, sequence queries** with invariants beyond no-panic.
- [ ] **Step 3: Test secret marker absence in public error, DB bytes, live bus and release artifacts**.
- [ ] **Step 4: Fix real defects regression-first; unresolved HIGH/CRITICAL blocks**.
- [ ] **Step 5: Commit** A07 test/fix changes.

### Task 8: A08 Concurrency / Recovery / Backpressure

**Files:**
- Create: `internal/store/events_a08_concurrency_test.go`
- Create: `internal/events/bus_a08_test.go`
- Modify store/bus only when tests expose gaps.

**Interfaces:**
- Produces multi-store sequence correctness, bounded subscriber backpressure, idempotent lost-response recovery, restart resume.

- [ ] **Step 1: RED multi-store append test** proving unique monotonic sequence under contention.
- [ ] **Step 2: Prove duplicate idempotency key converges and mismatched replay conflicts**.
- [ ] **Step 3: Prove slow/dead subscriber cannot deadlock canonical append** according to TERRA backpressure rules.
- [ ] **Step 4: Prove reopen + `Since` reconstructs missed live events**.
- [ ] **Step 5: Commit** `fix(T43.A08): harden event concurrency and recovery` when production fixes exist.

### Task 9: A09 Performance / Observability / Docs

**Files:**
- Create: `internal/events/metrics.go`
- Create: `internal/events/benchmark_test.go`
- Update operator docs in repository-established location.

**Interfaces:**
- Produces bounded/non-authoritative append/publish/subscriber metrics and benchmark baselines.

- [ ] **Step 1: Benchmark append, `Since`, publish fanout, and bounded payload validation**.
- [ ] **Step 2: Add bounded metrics without event IDs/subjects/secrets as unbounded labels**.
- [ ] **Step 3: Verify observability cannot influence event authority/order**.
- [ ] **Step 4: Document resume/backpressure/recovery semantics and SLOs**.
- [ ] **Step 5: Commit** `feat(T43.A09): add event observability and benchmarks`.

### Task 10: A10 Integration / Release

**Files:**
- Add/modify T43 release-gate tests using existing integration conventions.
- Modify: `distribution/PACK-MANIFEST.json`.
- Update completion ledger/report.

**Interfaces:**
- Produces T43 epic release checkpoint for Wave 0 integration.

- [ ] **Step 1: Run T43 Definition of Done/security review**.
- [ ] **Step 2: Run focused/full/race/fuzz/backpressure/restart/secret suites** with fresh evidence.
- [ ] **Step 3: Regenerate release pack and verify deterministic output**.
- [ ] **Step 4: Record exact A01-A10 SHAs/results**.
- [ ] **Step 5: Commit** `chore(T43.A10): finalize structured event stream release` and integrate only with zero unresolved HIGH/CRITICAL findings.
