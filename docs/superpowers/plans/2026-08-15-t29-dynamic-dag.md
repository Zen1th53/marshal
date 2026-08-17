# T29 Dynamic Task DAG Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement MARSHAL TERRA T29 as a persistent, cycle-safe, authority-gated dynamic task DAG whose readiness and history remain correct across retries, concurrency, and restart.

**Architecture:** Add a focused `internal/dag` contract/service layer backed by the existing SQLite `internal/store` migration and transaction framework. DAG state is canonical in SQLite; runtime integration consumes it through narrow methods, while events/evidence remain separate owning subsystems. Cycle detection and state transitions fail closed and never use process-local locks as the sole correctness boundary.

**Tech Stack:** Go 1.x, `context.Context`, existing `internal/store` SQLite layer (`modernc.org/sqlite`), existing `internal/model`, T06 evidence contracts, T48 policy contracts, standard Go tests/fuzz/race/benchmarks.

## Global Constraints

- Canonical baseline: `c1442e6a827fa3e14aa88c295ceaa8893551a151` plus approved completion-design commit.
- Author/committer for every commit: `Zen1th53 <extreme29@proton.me>`.
- A01→A10 remain serial inside T29.
- Provider/model labels never grant authority; fail closed on ambiguous/unavailable authority.
- Reuse existing migration/state/evidence/event interfaces; do not duplicate other epics.
- Durable correctness must rely on DB constraints/transactions/CAS, not only Go mutexes.
- Every production behavior change begins with a failing focused test.
- Keep `distribution/PACK-MANIFEST.json` current after tracked changes.
- Gemini/unavailable independent lanes are recorded as `BLOCKED / NOT EXECUTED`, never fabricated as PASS.

---

### Task 1: A01 Contract, Types, IDs, Errors

**Files:**
- Create: `internal/dag/types.go`
- Create: `internal/dag/errors.go`
- Create: `internal/dag/types_test.go`
- Create: `internal/dag/adversarial_test.go`

**Interfaces:**
- Produces: `Node`, `Edge`, `Graph`-facing interfaces, `NodeKind`, `NodeStatus`, `EdgeCondition`, `Code`, structured `Error`.
- Contract: `Node{TaskID,Kind,Status,Priority}`, `Edge{From,To,Condition}` and context-aware `AddNode`, `AddEdge`, `Ready`, `Topological` service methods.

- [ ] **Step 1: Write failing contract tests** for closed enums, safe errors, duplicate-edge semantics, malformed condition rejection, and defensive copies.

```go
func TestEdgeConditionRejectsUnknownValue(t *testing.T) {
    edge := Edge{From: "TASK-A", To: "TASK-B", Condition: EdgeCondition("unknown")}
    if err := edge.Validate(); !errors.Is(err, ErrInvalidCondition) {
        t.Fatalf("Validate() error = %v", err)
    }
}
```

- [ ] **Step 2: Confirm RED** with `go test ./internal/dag -run 'TestEdgeConditionRejectsUnknownValue|TestNodeContract' -count=1`.
- [ ] **Step 3: Implement minimal closed contracts** with `DAG_CYCLE`, `DAG_NODE_NOT_FOUND`, `DAG_DUPLICATE_EDGE`, `DAG_INVALID_CONDITION` and human-safe messages.
- [ ] **Step 4: Confirm GREEN** with `go test ./internal/dag -count=1`.
- [ ] **Step 5: Run no-leak + validation checks** and commit `feat(T29.A01): define dynamic DAG contracts`.

### Task 2: A02 Persistence, Migration, Indexes

**Files:**
- Modify: `internal/store/migrations.go`
- Create: `internal/store/dag.go`
- Create: `internal/store/dag_a02_test.go`
- Modify: `internal/doctor/doctor.go` only if schema version reporting requires it.

**Interfaces:**
- Consumes: `dag.Node`, `dag.Edge`.
- Produces: canonical node/edge persistence with forward/reverse indexes and immutable/conflict-safe inserts.

- [ ] **Step 1: Write failing migration/restart test** proving node/edge round-trip and reverse lookup across reopen.
- [ ] **Step 2: Confirm RED** against current latest schema.
- [ ] **Step 3: Add next legal migration** for `dag_nodes(task_id,kind,status,priority,revision,...)` and `dag_edges(from_task,to_task,condition,...)` with unique edge identity and indexes both directions.
- [ ] **Step 4: Implement transactional store methods**; validate IDs/enums before writes and return typed conflicts.
- [ ] **Step 5: Run clean-db + upgrade + integrity tests** and commit `feat(T29.A02): persist dynamic task DAG`.

### Task 3: A03 Core State Machine and Graph Service

**Files:**
- Create: `internal/dag/engine.go`
- Create: `internal/dag/engine_test.go`
- Modify: `internal/store/dag.go`

**Interfaces:**
- Produces: explicit transitions `pending -> ready -> running -> completed|failed|blocked|skipped`; `AddNode`, `AddEdge`, `Ready`, `Topological`.

- [ ] **Step 1: Write RED test** that adding `B -> A` after `A -> B` returns `ErrCycle` and persists neither edge nor contradictory audit state.
- [ ] **Step 2: Implement cycle-safe mutation** within one canonical transaction/CAS boundary.
- [ ] **Step 3: Implement readiness** requiring every mandatory inbound condition to be satisfied.
- [ ] **Step 4: Implement deterministic topological ordering** with stable tie-breaker from repository conventions.
- [ ] **Step 5: Exhaustively test illegal state transitions** for zero side effects and commit `feat(T29.A03): implement DAG state machine`.

### Task 4: A04 Security Boundary

**Files:**
- Create: `internal/dag/authorization.go`
- Create: `internal/store/dag_authorization_a04_test.go`
- Modify: `internal/dag/engine.go`

**Interfaces:**
- Produces: typed mutation request/decision and narrow `Authorizer`/freshness boundary for security-sensitive graph mutation.

- [ ] **Step 1: RED test** proves missing authorizer cannot mutate graph.
- [ ] **Step 2: Bind authorization** to canonical subject/session/task/change/action/node/edge/revision facts required by TERRA.
- [ ] **Step 3: Revalidate freshness/state immediately before transactional mutation**; stale decisions fail closed.
- [ ] **Step 4: Attack self-authorization/provider spoof/replay**; policy content and provider names never grant DAG-management authority.
- [ ] **Step 5: Commit** `feat(T29.A04): enforce DAG security boundary`.

### Task 5: A05 Events, Evidence, Audit

**Files:**
- Create: `internal/store/dag_events.go`
- Create: `internal/store/dag_events_a05_test.go`
- Modify: `internal/dag/engine.go`

**Interfaces:**
- Consumes: existing audit/evidence contracts.
- Produces: bounded, machine-readable facts for successful graph changes without making evidence historical facts into authority.

- [ ] **Step 1: RED atomicity test** forces event/audit failure and proves graph mutation rolls back when TERRA requires coupled persistence.
- [ ] **Step 2: Emit only canonical event types/reasons** after durable success; deny/failure behavior follows registries.
- [ ] **Step 3: Bind evidence to exact task/edge/state/revision operation**, sanitize metadata, and forbid raw secrets.
- [ ] **Step 4: Test retry/lost-response semantics** for one semantic success.
- [ ] **Step 5: Commit** `feat(T29.A05): integrate DAG events and evidence`.

### Task 6: A06 Runtime / CLI / Protocol Integration

**Files:**
- Modify: `internal/app/runtime.go`
- Create: `internal/app/dag_runtime.go`
- Create: `internal/app/dag_runtime_a06_test.go`
- Modify: protocol/CLI surfaces only where exact T29 contract requires it.

**Interfaces:**
- Produces: runtime task eligibility based on canonical DAG readiness, never on caller prose or provider claims.

- [ ] **Step 1: RED test** proves a task with unsatisfied predecessors cannot reach claim/provider execution.
- [ ] **Step 2: Wire canonical DAG readiness before side effects** in shared runtime entrypoint.
- [ ] **Step 3: Verify API/A2A/MCP/CLI delegation cannot bypass the shared gate**.
- [ ] **Step 4: Test restart and provider-neutral fake adapters**.
- [ ] **Step 5: Commit** `feat(T29.A06): integrate DAG runtime gating`.

### Task 7: A07 Adversarial / Fuzz Hardening

**Files:**
- Extend: `internal/dag/adversarial_test.go`
- Create as needed: `internal/dag/fuzz_test.go`, `internal/store/dag_a07_test.go`

**Interfaces:**
- Produces: regression corpus for cycle insertion, malformed IDs/conditions, replay, stale authority, bounds, corruption, secret containment.

- [ ] **Step 1: Build 30+ attack/property matrix** from T29 `09`, `10`, `16` specs.
- [ ] **Step 2: Add fuzz targets** for graph mutations, cycle detection, condition parsing, serialization/roundtrip.
- [ ] **Step 3: Run bounded live fuzz** and retain only meaningful regression corpus.
- [ ] **Step 4: Fix every real defect with regression-first TDD**; unresolved HIGH/CRITICAL blocks task.
- [ ] **Step 5: Commit** test/fix commits under T29.A07 scope.

### Task 8: A08 Concurrency / Idempotency / Recovery

**Files:**
- Create: `internal/store/dag_a08_concurrency_test.go`
- Modify: `internal/store/dag.go`, `internal/dag/engine.go` only when tests expose real gaps.

**Interfaces:**
- Produces: multi-store correctness, bounded contention behavior, idempotent retries, crash/reopen reconciliation.

- [ ] **Step 1: RED concurrency test** using two independent stores against one SQLite DB.
- [ ] **Step 2: Prove conflicting edge/state mutations have one canonical winner** using DB transaction/CAS constraints.
- [ ] **Step 3: Prove lost-response retry and pre-commit failure** do not duplicate semantic success/audit.
- [ ] **Step 4: Run repeated tests + race detector + integrity checks**.
- [ ] **Step 5: Commit** `fix(T29.A08): harden DAG concurrency and recovery` when production fixes exist, otherwise test-only A08 commit.

### Task 9: A09 Performance / Observability / Operator UX

**Files:**
- Create: `internal/dag/metrics.go`
- Create: `internal/dag/benchmark_test.go`
- Extend docs in the T29 operator-facing repository location established by current conventions.

**Interfaces:**
- Produces: bounded/non-authoritative metrics and benchmark baseline for add-edge, readiness, topological order, hot reverse-edge lookup.

- [ ] **Step 1: Add benchmark baselines** for representative graph sizes.
- [ ] **Step 2: Add bounded metrics** for mutation/ready/cycle-reject latency and outcomes; no IDs/secrets as unbounded labels.
- [ ] **Step 3: Verify metrics never influence authority/readiness**.
- [ ] **Step 4: Document operator semantics, failure modes, and SLO evidence**.
- [ ] **Step 5: Commit** `feat(T29.A09): add DAG observability and benchmarks`.

### Task 10: A10 Integration / Release

**Files:**
- Create/modify T29 release-gate tests under existing release/integration conventions.
- Modify: `distribution/PACK-MANIFEST.json` after legitimate tracked changes.
- Update: completion ledger/report.

**Interfaces:**
- Produces: T29 epic release checkpoint suitable for integration branch.

- [ ] **Step 1: Run T29 Definition of Done and cross-review checklist**.
- [ ] **Step 2: Run focused, full, race, fuzz, integrity, secret, and provider-neutral suites** with fresh evidence.
- [ ] **Step 3: Regenerate and deterministically verify release pack**.
- [ ] **Step 4: Record exact atomic SHAs/results in completion ledger**.
- [ ] **Step 5: Commit** `chore(T29.A10): finalize dynamic DAG release` and integrate only if no unresolved HIGH/CRITICAL findings remain.
