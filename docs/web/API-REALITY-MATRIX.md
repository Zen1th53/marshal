# MARSHAL Web Control Plane — API Reality & Boundary Matrix

This document provides the definitive, audit-verified reality classification for every HTTP API endpoint exposed by the MARSHAL Web Control Plane (`internal/webcontrol/`).

---

## Reality Taxonomy

* **`REAL (CANONICAL)`**: Backed by live SQLite database tables, real cryptographic tokens, real worktree management, active policy gates, and genuine sandboxed execution.
* **`DEV-DEMO (FIXTURE)`**: Operates on live thread-safe memory stores or fixtures when running in test / standalone developer mode without an attached daemon runtime.
* **`NOT IMPLEMENTED (501)`**: Explicitly rejects execution with `501 Not Implemented` rather than returning a simulated success.

---

## Route Classification Matrix

| Route | Method | Classification | Backing System | Auth / Policy Required |
| :--- | :--- | :--- | :--- | :--- |
| `/api/v1/system/status` | `GET` | `REAL (CANONICAL)` | Runtime layout, database schema inspection | Public (Read-Only) |
| `/api/v1/system/adapters` | `GET` | `REAL (CANONICAL)` | Probed system CLI binaries (`bwrap`, `codex`, `opencode`) | Public (Read-Only) |
| `/api/v1/system/capabilities` | `GET` | `REAL (CANONICAL)` | Bubblewrap sandbox probe & system capabilities | Public (Read-Only) |
| `/api/v1/overview` | `GET` | `REAL (CANONICAL)` | Aggregated metrics & `DynamicTaskStore` live counts | Public (Read-Only) |
| `/api/v1/auth/login` | `POST` | `REAL (CANONICAL)` | Cryptographic OTP token exchange & `SessionStore` | Public |
| `/api/v1/auth/me` | `GET` | `REAL (CANONICAL)` | Validated `marshal_session` cookie lookup | Authenticated Session |
| `/api/v1/auth/logout` | `POST` | `REAL (CANONICAL)` | Session revocation & cookie invalidation | Authenticated Session + CSRF |
| `/api/v1/auth/csrf` | `GET` | `REAL (CANONICAL)` | Cryptographically generated HMAC-SHA256 token | Authenticated Session |
| `/api/v1/events/stream` | `GET` | `REAL (CANONICAL)` | Server-Sent Events (`SSEHub`) broadcast channel | Authenticated Session |
| `/api/v1/agents` | `GET` | `REAL (CANONICAL)` | Agent registry / database lookup | Authenticated Session |
| `/api/v1/agents/{id}` | `GET` | `REAL (CANONICAL)` | Agent detail and capability lookup | Authenticated Session |
| `/api/v1/tasks` | `GET` | `REAL (CANONICAL)` | Canonical `store.ListTasks` / `DynamicTaskStore` | Authenticated Session |
| `/api/v1/tasks` | `POST` | `REAL (CANONICAL)` | `store.ImportTasks` / `DynamicTaskStore.Create` | `task.plan` + CSRF |
| `/api/v1/tasks/dag` | `GET` | `REAL (CANONICAL)` | Topological DAG layout engine | Authenticated Session |
| `/api/v1/tasks/{id}` | `GET` | `REAL (CANONICAL)` | Comprehensive task detail & state inspection | Authenticated Session |
| `/api/v1/tasks/{id}` | `PATCH` | `REAL (CANONICAL)` | Task metadata update with CAS revision check | `task.plan` + CSRF |
| `/api/v1/tasks/{id}/claim` | `POST` | `REAL (CANONICAL)` | Atomic task claim & session generation | `task.plan` + CSRF |
| `/api/v1/tasks/{id}/run` | `POST` | `REAL (CANONICAL)` | Task execution trigger & run dispatcher | `task.plan` + CSRF |
| `/api/v1/tasks/{id}/cancel` | `POST` | `REAL (CANONICAL)` | Task cancellation & lease release | `task.plan` + CSRF |
| `/api/v1/tasks/{id}/retry` | `POST` | `REAL (CANONICAL)` | Task retry & state reset | `task.plan` + CSRF |
| `/api/v1/runs` | `GET` | `REAL (CANONICAL)` | Worker runs database table | Authenticated Session |
| `/api/v1/runs/{id}` | `GET` | `REAL (CANONICAL)` | Worker run detail & execution boundary metadata | Authenticated Session |
| `/api/v1/runs/{id}/logs` | `GET` | `REAL (CANONICAL)` | Sanitized streaming run logs from artifact store | Authenticated Session |
| `/api/v1/runs/{id}/result` | `GET` | `REAL (CANONICAL)` | Run result commit & exit code summary | Authenticated Session |
| `/api/v1/runs/{id}/boundary` | `GET` | `REAL (CANONICAL)` | Sandboxed execution boundary & egress audit | Authenticated Session |
| `/api/v1/runs/{id}/recover` | `POST` | `REAL (CANONICAL)` | Crash recovery and lease cleanup | `task.plan` + CSRF |
| `/api/v1/artifacts/{id}/download` | `GET` | `REAL (CANONICAL)` | Immutable content-addressed artifact store | `verify.qa` |
| `/api/v1/review/queue` | `GET` | `REAL (CANONICAL)` | Review queue inspection | Authenticated Session |
| `/api/v1/tasks/{id}/quorum` | `GET` | `REAL (CANONICAL)` | Quorum workspace & attestation status | Authenticated Session |
| `/api/v1/tasks/{id}/quorum/decision` | `POST` | `REAL (CANONICAL)` | Multi-party quorum attestation submission | `verify.qa` + CSRF |
| `/api/v1/tasks/{id}/merge/preflight` | `GET` | `REAL (CANONICAL)` | Merge gate validation & checks | Authenticated Session |
| `/api/v1/tasks/{id}/merge` | `POST` | `REAL (CANONICAL)` | Git merge execution & finalization | `release.approve` + CSRF |
| `/api/v1/evidence` | `GET` | `REAL (CANONICAL)` | Immutable evidence graph queries | `verify.qa` |
| `/api/v1/evidence/{id}` | `GET` | `REAL (CANONICAL)` | Evidence node & edge detail | `verify.qa` |
| `/api/v1/provenance/trace` | `GET` | `REAL (CANONICAL)` | Full "Why?" provenance trail from commit to task | Authenticated Session |
| `/api/v1/providers` | `GET` | `REAL (CANONICAL)` | Provider availability & router weights | Authenticated Session |
| `/api/v1/providers/router/override` | `POST` | `REAL (CANONICAL)` | Dynamic model routing overrides | `policy.admin` + CSRF |
| `/api/v1/security/policy` | `GET` | `REAL (CANONICAL)` | Active security policy & rule inspection | `policy.admin` |
| `/api/v1/audit/events` | `GET` | `REAL (CANONICAL)` | Audit trail query with outcome filters | `policy.admin` |
| `/api/v1/audit/export` | `GET` | `REAL (CANONICAL)` | Streaming tamper-evident JSON audit export | `policy.admin` |
| `/api/v1/memory/search` | `GET` | `REAL (CANONICAL)` | Hybrid vector + lexical memory search | Authenticated Session |
| `/api/v1/memory/retrieval/explain` | `GET` | `REAL (CANONICAL)` | RRF fusion explainability & score breakdown | Authenticated Session |
| `/api/v1/memory/governance/queue` | `GET` | `REAL (CANONICAL)` | Memory governance & conflict queue | `policy.admin` |
| `/api/v1/memory/governance/conflicts/{id}` | `GET` | `REAL (CANONICAL)` | Conflict comparison & diff analysis | `policy.admin` |
| `/api/v1/memory/working` | `GET` | `REAL (CANONICAL)` | Working memory slot inspector | Authenticated Session |
| `/api/v1/memory/working/slot` | `POST` | `REAL (CANONICAL)` | Working memory slot mutation | `source.write` + CSRF |
| `/api/v1/memory/working/promote` | `POST` | `REAL (CANONICAL)` | Working slot promotion to long-term memory | `source.write` + CSRF |
| `/api/v1/memory/mutations/promote` | `POST` | `REAL (CANONICAL)` | Memory record promotion mutation | `policy.admin` + CSRF |
| `/api/v1/memory/mutations/supersede` | `POST` | `REAL (CANONICAL)` | Memory record supersede mutation | `policy.admin` + CSRF |
| `/api/v1/memory/mutations/tombstone` | `POST` | `REAL (CANONICAL)` | Memory record tombstone mutation | `policy.admin` + CSRF |
| `/api/v1/memory/versioning/snapshots` | `GET` | `REAL (CANONICAL)` | Memory version snapshots list | `policy.admin` |
| `/api/v1/memory/versioning/snapshots` | `POST` | `REAL (CANONICAL)` | Memory version snapshot creation | `policy.admin` + CSRF |
| `/api/v1/memory/versioning/diff` | `GET` | `REAL (CANONICAL)` | Snapshot diff analysis | `policy.admin` |
| `/api/v1/memory/versioning/rollback` | `POST` | `REAL (CANONICAL)` | Memory snapshot rollback execution | `policy.admin` + CSRF |
| `/api/v1/memory/{id}` | `GET` | `REAL (CANONICAL)` | Memory record lookup | Authenticated Session |
| `/api/v1/memory/{id}/detail` | `GET` | `REAL (CANONICAL)` | Memory record detail & lifecycle history | Authenticated Session |
| `/api/v1/memory/{id}/usage` | `GET` | `REAL (CANONICAL)` | Memory usage trace & influence receipts | Authenticated Session |
| `/api/v1/memory/security/health` | `GET` | `REAL (CANONICAL)` | Memory security ACL & integrity scanner | `policy.admin` |
| `/api/v1/health/doctor` | `GET` | `REAL (CANONICAL)` | System diagnostics (`doctor.RunChecks`) | Public (Read-Only) |
| `/api/v1/resources` | `GET` | `REAL (LOCAL ADVISORY)` | Bounded host/Ollama resource snapshot | Authenticated Session |
| `/api/v1/operations/backups` | `GET` | `REAL (CANONICAL)` | `store.ListBackups` artifact directory query | `policy.admin` |
| `/api/v1/operations/backups/create` | `POST` | `REAL (CANONICAL)` | `store.Backup` atomic WAL checkpointing | `policy.admin` + CSRF |
| `/api/v1/operations/backups/verify` | `POST` | `REAL (CANONICAL)` | `store.VerifyBackup` SQLite PRAGMA check | `policy.admin` + CSRF |
| `/api/v1/operations/backups/restore` | `POST` | `REAL (CANONICAL)` | `store.RestoreDatabase` atomic restoration | `policy.admin` + CSRF |
| `/api/v1/operations/maintenance/jobs` | `GET` | `REAL (CANONICAL)` | Maintenance job history query | `policy.admin` |
| `/api/v1/operations/maintenance/jobs` | `POST` | `REAL (CANONICAL)` | SQLite VACUUM / WAL GC / Reindex execution | `policy.admin` + CSRF |
| `/api/v1/operations/trust` | `GET` | `REAL (CANONICAL)` | Structured trust report & release readiness | Public (Read-Only) |
| `/api/v1/benchmarks` | `GET` | `REAL (CANONICAL)` | Benchmark metrics summary | Public (Read-Only) |
| `/api/v1/settings` | `GET` | `REAL (CANONICAL)` | System configuration & CAS revision | `policy.admin` |
| `/api/v1/settings` | `PUT` | `REAL (CANONICAL)` | System configuration update with CAS check | `policy.admin` + CSRF |
| `/api/v1/search` | `GET` | `REAL (CANONICAL)` | Global search across tasks, runs, agents, memory | Authenticated Session |

---

## Security Invariants

1. **Deny Anonymous Loopback Authority**: Loopback requests receive zero privileged authorities without an explicit authenticated session cookie.
2. **Double-Submit CSRF Protection**: All state-changing requests (`POST`, `PATCH`, `PUT`, `DELETE`) require a valid session-bound `X-CSRF-Token` header.
3. **Fail-Closed Authorization**: Any request with an expired session or missing authority is immediately rejected with `401 Unauthorized` or `403 Forbidden`.
