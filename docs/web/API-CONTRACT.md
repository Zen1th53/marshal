# MARSHAL Web Control Plane — API Contract & DTO Specification

**Specification Version:** `1.0.0`  
**Task:** T166  

This document formalizes the complete JSON HTTP contract for the MARSHAL Web Control Plane.

---

## 1. Common Conventions

- **Encoding:** `application/json; charset=utf-8`
- **Timestamps:** ISO-8601 / RFC-3339 in UTC (`2026-08-19T12:00:00Z`)
- **Identifiers:** Opaque URL-safe strings (`TASK-001`, `MEM-8a9b0c`)
- **Pagination:** Cursor-based pagination with `limit` (default 50, maximum 100) and `next_cursor`
- **Mutations:** Include optional `idempotency_key` and `expected_revision` for CAS safety.

---

## 2. Core Resource Endpoints

### 2.1 System Diagnostics & Health
- `GET /api/v1/system/status` -> `SystemStatusDTO`
- `GET /api/v1/system/adapters` -> `[]AdapterSummaryDTO`

### 2.2 Agents
- `GET /api/v1/agents` -> `PagedResponse[AgentSummaryDTO]`
- `POST /api/v1/agents` -> `MutationEnvelope[AgentCreateRequest]` -> `AgentSummaryDTO`

### 2.3 Tasks & Runs
- `GET /api/v1/tasks` -> `PagedResponse[TaskSummaryDTO]`
- `POST /api/v1/tasks/import` -> `[]TaskImportDTO` -> `TaskImportResultDTO`
- `POST /api/v1/tasks/:id/run` -> `TaskRunTriggerDTO` -> `RunSummaryDTO`
- `POST /api/v1/tasks/:id/cancel` -> `TaskCancelRequestDTO` -> `TaskSummaryDTO`

### 2.4 Institutional Memory
- `GET /api/v1/memory/search?q=...` -> `PagedResponse[MemoryRecordDTO]`
- `GET /api/v1/memory/:id` -> `MemoryRecordDTO`
- `POST /api/v1/memory/:id/mutate` -> `MutationEnvelope[MemoryMutationDTO]`

### 2.5 Realtime SSE Stream
- `GET /api/v1/events/stream` (SSE: `text/event-stream`)
