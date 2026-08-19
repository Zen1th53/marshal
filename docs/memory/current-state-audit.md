# MARSHAL Memory System — Current State Audit

**Produced by:** T77 — Memory Baseline Audit and Authority Map
**Baseline commit:** f9dd8d1 (T56–T76 PASS confirmed)
**Schema version:** v67

## 1. Canonical Memory Tables

### 1.1 `memory_records` — canonical-legacy
- **Schema version introduced:** initial schema (v1, `migrations.go` line 162)
- **Model:** `internal/model/policy.go` → `type MemoryRecord struct`
- **Write path:** `internal/store/policy_records.go` → `func (s *Store) Remember(ctx, model.MemoryRecord) error`
- **Read path:** none — no `Recall` or query function implemented
- **Fields:** `memory_id`, `project_id`, `memory_type` (free-form string), `status` (free-form string), `confidence` (free-form string), `body`, `provenance_json`, `last_verified_at`, `revision`, `created_at`, `updated_at`
- **Gaps:**
  - free-form string enums (not validated at DB boundary)
  - no `title`, `scope`, `scope_id`, `evidence_ids`, `content_digest`
  - no temporal fields: `observed_at`, `valid_from`, `valid_to`, `ingested_at`
  - no `authority_class`, `superseded_by`, `conflict_ids`
  - no read/recall implementation
  - memory kind enum misses: `handoff`, `checkpoint`, `failure`
  - lifecycle enum misses: `observed`, `candidate`, `verified`, `durable`, `tombstoned`, `conflicted`
- **Classification:** canonical-legacy (sole authoritative table for general memory)
- **Convergence destination:** will become `memory_records_v2` in T78/T79

### 1.2 `persistent_agent_memory` — canonical-legacy (agent-scoped)
- **Schema version introduced:** v29 (`migrations.go` line 797)
- **Model:** none — no Go struct defined
- **Write path:** none — table created but no Go write path
- **Read path:** none
- **Fields:** `entry_id`, `scope`, `scope_id`, `kind`, `text`, `confidence`, `source_evidence_ids`, `superseded_by`, `created_at`, `updated_at`
- **Gaps:**
  - no Go model struct
  - no write or read path implemented
  - schema diverges from `memory_records` (agent-scoped, simpler)
  - confidence stored as REAL (not enum)
  - no temporal fields
- **Classification:** canonical-legacy (isolated, no existing consumer)
- **Convergence destination:** will be migrated into `memory_records_v2` projection in T79

### 1.3 `decision_records` — canonical-legacy (task-scoped decisions)
- **Schema version introduced:** v32 (`migrations.go` line 874)
- **Model:** `internal/memory/decision/engine.go` → `type DecisionRecord struct` (in-memory only, NOT persisted)
- **Write path:** none (the in-memory `decision.Engine` is not connected to this table)
- **Read path:** none
- **Fields:** `decision_id`, `task_id`, `agent_id`, `title`, `context`, `decision`, `consequences`, `status`, `superseded_by`, `created_at`, `updated_at`
- **Gaps:**
  - in-memory `decision.Engine` and SQLite `decision_records` are completely disconnected
  - no Go model for the DB record
  - no write/read path connecting engine to store
  - no evidence binding
- **Classification:** canonical-legacy (disconnected island)
- **Convergence destination:** will be bridged and migrated in T79

### 1.4 `failure_memory_records` — canonical-legacy (failure patterns)
- **Schema version introduced:** v33 (`migrations.go` line 899)
- **Model:** none
- **Write path:** none
- **Read path:** none
- **Fields:** `failure_id`, `scope_id`, `task_type`, `approach`, `root_cause`, `resolution`, `signature`, `evidence_ids`, `severity`, `status`, `created_at`
- **Gaps:**
  - no Go model
  - no write/read path
  - severity and status are free-form strings
- **Classification:** canonical-legacy (schema-only, no consumer)
- **Convergence destination:** will be migrated into `memory_records_v2` in T79

## 2. Derived / Non-Canonical Tables (memory-adjacent)

These are NOT canonical memory stores. They are decision logs for runtime subsystems:

| Table | Migration | Purpose | Classification |
|---|---|---|---|
| `decisions` | v1 | runtime operational decisions (legacy) | subsystem-derived |
| `gate_decisions` | v20 | security gate outcomes | subsystem-derived |
| `egress_decisions` | v23 | network egress policy outcomes | subsystem-derived |
| `context_budget_decisions` | v38 | context budget allocations | subsystem-derived |
| `model_router_decisions` | v47 | model selection logs | subsystem-derived |

These must not be merged into the canonical memory store.

## 3. Non-Persistent Memory Components

### 3.1 `internal/memory/decision.Engine`
- In-memory map of `DecisionRecord`
- Exports: `Propose`, `Accept`, `Reject`, `Supersede`, `List`
- NOT connected to SQLite `decision_records`
- Test-covered in isolation

### 3.2 `internal/context/compiler`
- `CompiledContext.MemoryIDs []string` — reference IDs only, no actual retrieval
- No read from `memory_records` implemented

## 4. Schema JSON

`schemas/memory-record.schema.json` partially matches design spec:
- memory_type enum missing: `handoff`, `checkpoint`, `failure`
- status enum missing: `observed`, `candidate`, `verified`, `durable`, `tombstoned`, `conflicted`
- No temporal fields: `valid_from`, `valid_to`, `observed_at`, `ingested_at`
- No `scope`, `scope_id`, `evidence_ids`, `content_digest`, `authority_class`, `superseded_by`

## 5. Documentation State

| File | Status |
|---|---|
| `runtime/MEMORY-SERVICE.md` | describes intended API; actual implementation incomplete |
| `memory/MEMORY.md` | high-level design |
| `schemas/memory-record.schema.json` | partial, needs extension |

## 6. Convergence Decision

**Authoritative convergence destination: `memory_records_v2`**

The canonical table for all durable memory will be `memory_records_v2` introduced in T78.
All existing legacy tables (`persistent_agent_memory`, `decision_records`, `failure_memory_records`) will be migrated into typed projection views in T79.
The original `memory_records` table will be preserved during migration window then superseded.

## 7. Required Actions Before T78

1. T77 establishes this audit document as the authority map.
2. T77 adds a schema inventory test that fails if a new canonical-looking memory table is added without classification in this document.
3. T78 will define `internal/model/memory.go` with typed enums for the v2 contract.
4. T79 will run migration from all four legacy tables to v2.

## 8. Known Limitations

- `persistent_agent_memory` has no Go consumer — safe to converge immediately.
- `decision_records` disconnect from `decision.Engine` must be explicitly bridged in T79.
- No read path exists yet for `memory_records` — T80 will implement the canonical read/recall store.
