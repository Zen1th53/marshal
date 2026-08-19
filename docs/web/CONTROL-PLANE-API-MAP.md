# MARSHAL Web Control Plane — API Capability Map

**Audit Date:** 2026-08-19  
**Source Baseline:** Main branch post-T164 (`5668671`)  
**Task:** T165  

This document maps all operator-facing UI capabilities to their canonical Go runtime entry points, authorization capabilities, touched storage models, DTO conversion requirements, and security classification.

---

## 1. Domain Capability Mappings

### 1.1 Overview & System Diagnostics
| UI Action / View | Canonical Go Entry Point | Auth Capability | Classification | Touched State | Redaction / Constraints |
|---|---|---|:---:|---|---|
| System Status Overview | `doctor.RunDiagnostics(ctx, store)` | `cap:system:read` | **READY** | SQLite system health, outbox queue | Strip internal file paths |
| Discovered Provider Adapters | `doctor.ProbeAdapters(ctx)` | `cap:adapter:read` | **READY** | Runtime adapter registry | Zero API keys / secrets exposed |
| Active Tasks & Worker Pools | `app.Runtime.ListTasks(ctx)` + `worker.Manager` | `cap:task:read` | **READY** | `tasks`, worker process leases | Bounded page size (default 50) |

### 1.2 Agents & Capability Leases
| UI Action / View | Canonical Go Entry Point | Auth Capability | Classification | Touched State | Redaction / Constraints |
|---|---|---|:---:|---|---|
| List Registered Agents | `app.Runtime.ListAgents(ctx)` | `cap:agent:read` | **READY** | `agents` table in SQLite | Mask private metadata |
| Register / Update Agent | `app.Runtime.RegisterAgent(ctx, input)` | `cap:agent:write` | **READY** | `agents` table | Audited event emitted |
| Inspect Capability Leases | `capability.Broker.ListActiveLeases(ctx)` | `cap:capability:read` | **READY** | In-memory lease table | Mask socket paths |

### 1.3 Tasks, Runs & DAG Execution
| UI Action / View | Canonical Go Entry Point | Auth Capability | Classification | Touched State | Redaction / Constraints |
|---|---|---|:---:|---|---|
| List / Filter Tasks | `app.Runtime.ListTasks(ctx, filter)` | `cap:task:read` | **READY** | `tasks` table | Server-side status/risk filter |
| Import Task Manifest | `app.Runtime.ImportTasks(ctx, tasks)` | `cap:task:write` | **READY** | `tasks` table | Strict schema validation |
| Trigger Task Run | `app.Runtime.RunTask(ctx, taskID, adapter)` | `cap:task:run` | **READY** | `tasks`, `runs`, sandbox cell | Audited mutation event |
| Cancel Active Task | `app.Runtime.CancelTask(ctx, taskID)` | `cap:task:cancel` | **READY** | Process supervisor (`kill -15`) | Graceful SIGTERM with timeout |
| Stream / View Task Logs | `app.Runtime.GetTaskLogs(ctx, taskID)` | `cap:log:read` | **READY** | Task log buffer | High-entropy secret redaction |

### 1.4 Review Gates, Evidence & Verification
| UI Action / View | Canonical Go Entry Point | Auth Capability | Classification | Touched State | Redaction / Constraints |
|---|---|---|:---:|---|---|
| List Pending Gate Reviews | `gate.Evaluator.ListPendingReviews(ctx)` | `cap:gate:read` | **READY** | `gate_reviews` table | Bounded list |
| Approve / Veto Gate Review | `gate.Evaluator.SubmitDecision(ctx, req)` | `cap:gate:approve` | **READY** | `gate_reviews`, `events` | Security officer role check |
| Inspect Evidence Tree | `evidence.Collector.GetEvidenceTree(ctx, id)` | `cap:evidence:read` | **READY** | `evidence` table | Cryptographic digest binding |

### 1.5 Institutional Memory Explorer
| UI Action / View | Canonical Go Entry Point | Auth Capability | Classification | Touched State | Redaction / Constraints |
|---|---|---|:---:|---|---|
| Search Canonical Memory | `app.Runtime.SearchMemory(ctx, query)` | `cap:memory:read` | **READY** | `memory_records_v2`, FTS, Vector | Direct-ID & scope ACL filtering |
| Inspect Memory Record | `app.Runtime.GetMemoryRecord(ctx, id)` | `cap:memory:read` | **READY** | `memory_records_v2` | AES-GCM decryption via Vault |
| Mutate / Tombstone Record | `app.Runtime.MutateMemory(ctx, mutReq)` | `cap:memory:write` | **READY** | Signed mutation envelope | Sycophancy & authority check |
| Snapshot / Branch / Rollback | `app.Runtime.ManageSnapshot(ctx, req)` | `cap:memory:admin` | **READY** | `memory_snapshots` table | Linear revision validation |
| View Grounded Evidence Plan | `evidenceplan.Planner.BuildPlan(ctx, ...)` | `cap:memory:read` | **READY** | Derived prompt plan | XML entity delimiter armor |

### 1.6 Audit, Security & Benchmarks
| UI Action / View | Canonical Go Entry Point | Auth Capability | Classification | Touched State | Redaction / Constraints |
|---|---|---|:---:|---|---|
| Stream Audit Event Log | `events.Streamer.Subscribe(ctx, filter)` | `cap:audit:read` | **READY** | `events` append-only log | SSE ordered stream with replay |
| Run Memory Benchmark Suite | `memory.NewBenchmarkRunner().RunBenchmark()`| `cap:bench:run` | **READY** | Isolated benchmark harness | Never commits to durable store |
| Index Rebuild Trigger | `app.Runtime.RebuildIndexes(ctx)` | `cap:admin:rebuild` | **READY** | Vector/FTS/Graph indexes | Rebuild parity validation |

---

## 2. Sensitive Fields (Strict Never-Expose Rules)

The following fields must **never** be serialized to browser JSON, SSE payloads, or HTML:
1. `private_key`, `hmac_secret_key`, `master_vault_key`
2. `bearer_token_raw`, `api_token_secret`, `session_secret`
3. `database_file_descriptor`, `internal_socket_path`
4. Unsanitized raw agent outputs attempting delimiter escape (must pass through `T164` armor).
