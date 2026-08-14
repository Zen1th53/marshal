# MARSHAL Runtime v0.3.0 Architecture Design

**Title**: MARSHAL Runtime v0.3.0 — Production-Hardened Local Control Plane
**Status**: APPROVED / IMPLEMENTATION
**Version**: 0.3.0
**Target Date**: 2026-08-12

---

## 1. Executive Summary

MARSHAL Runtime v0.3.0 hardens the local multi-agent control plane against crashes, orphaned provider process leaks, race conditions, unauthorized protocol requests, and secret leakage in logs/events. Additionally, it establishes full real-world interoperability for OpenCode executing against local Ollama models alongside existing Codex production verification.

---

## 2. Architecture & Design Principles

### 2.1 Canonical Execution Path
All incoming task execution requests (CLI, MCP 2026-07-28, A2A 1.0) MUST flow through the single canonical `runtime.Run` pipeline:
```
CLI / MCP / A2A -> Authentication -> Authorization/Policy -> Lease Ownership -> Task Worktree -> Process Supervisor (bwrap/process group) -> Provider Adapter -> Evidence & Events -> SQLite Store
```

### 2.2 Execution State Machine & SQLite Schema Migration
Persisted task execution states:
- `QUEUED`
- `CLAIMED`
- `PREPARING`
- `RUNNING`
- `VERIFYING`
- `COMPLETED`
- `FAILED`
- `CANCELLING`
- `CANCELLED`
- `RECOVERY_REQUIRED`

Database schema upgrade from version `1` to `2` extends `executions` table with:
- `runtime_instance_id` (UUID identifying daemon session)
- `process_start_identity` (PID + boot identity / start time)
- `cancellation_requested_at`
- `recovery_epoch`

### 2.3 Process Supervisor & Process Tree Ownership
The `ProcessSupervisor` wraps child process execution inside a process group (Linux `pgid` / `bwrap` namespace).
- Graceful cancellation sequence: `SIGTERM` -> grace period (3s) -> `SIGKILL` if still alive.
- Daemon startup reconciliation: scans non-terminal tasks from database, checks runtime instance identity and process tree existence. If process is gone or owner is dead, transitions to `RECOVERY_REQUIRED` or clean `CANCELLED`, terminating orphan process trees safely without deleting worktree evidence.

### 2.4 Unified Principal Model & Authentication Boundary
Principals:
- `Kind`: `local_user`, `mcp_client`, `a2a_agent`
- High-entropy bearer tokens (`marshal auth token create/list/revoke`) hashed using SHA-256 and verified using constant-time comparison (`subtle.ConstantTimeCompare`).
- MCP 2026-07-28 and A2A 1.0 HTTP servers require `Authorization: Bearer <token>`. Unauthorized requests return HTTP 401 Unauthorized / HTTP 403 Forbidden without launching workers or creating leases.

### 2.5 Secrets Boundary & Redaction
- `SecretResolver` resolves `env:<VAR>` and `file:<PATH>` references. File secrets require regular non-world-readable files.
- Stdout, stderr, and event streams pass through known-secret redaction filters replacing plaintext secrets with `[REDACTED]`.

---

## 3. Provider Adapter Hardening

### 3.1 Codex Adapter
- Production supported, real E2E verified via `MARSHAL_TEST_REAL_CODEX=1`.

### 3.2 OpenCode + Ollama Adapter
- Executable binary: `opencode` (stable 1.18.x).
- Provider: `ollama` (`http://localhost:11434`).
- Non-interactive execution: `opencode run --model ollama/<model> --auto --dir <worktree>`.
- Real E2E verified via `MARSHAL_TEST_REAL_OPENCODE=1`.

---

## 4. Verification Plan

1. Deterministic Go unit and integration tests under `-race`.
2. Python conformance and pack validation suites.
3. Distribution pack manifest validation (`python3 tools/release_verify.py`).
4. Real Codex E2E suite (`MARSHAL_TEST_REAL_CODEX=1`).
5. Real OpenCode/Ollama E2E suite (`MARSHAL_TEST_REAL_OPENCODE=1`).
