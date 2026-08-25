# Web API reality matrix

This is the v1.0.1 production boundary. `marshal web serve` always attaches a
canonical runtime. A nil-runtime server exists only for explicit Go tests/demo
fixtures.

| Route group | Live-runtime status | Backing |
|---|---|---|
| Static SPA assets | AVAILABLE | Embedded `internal/webcontrol/dist` |
| `/api/v1/auth/*` | AVAILABLE | Ephemeral one-time codes and server-side sessions |
| `/api/v1/system/status` | AVAILABLE | Build metadata and canonical runtime status |
| `/api/v1/resources` | AVAILABLE, authenticated | Bounded local Community resource snapshot |
| `/api/v1/health/doctor` | AVAILABLE | SQLite, memory, SSE, and Bubblewrap diagnostics |
| `/api/v1/memory/search` | AVAILABLE, authenticated | Canonical `MemoryService` / `memory_records_v2` |
| `/api/v1/memory/retrieval/explain` | AVAILABLE, authenticated | Canonical bounded recall receipt |
| `/api/v1/memory/working*` | AVAILABLE, authorized | Canonical task working-memory service |
| `/api/v1/memory/mutations/*` | AVAILABLE, policy-admin | Canonical CAS-governed mutations |
| `/api/v1/memory/{id}` and `/{id}/detail` | AVAILABLE, authenticated | Scope-authorized canonical record lookup |
| `/api/v1/operations/backups` list/create/verify | AVAILABLE, policy-admin | Real SQLite backups and verification |
| `/api/v1/operations/backups/restore` | `501 NOT IMPLEMENTED` | Live online restore is intentionally refused; use offline CLI restore |
| Other registered `/api/v1/*` routes | `501 NOT IMPLEMENTED` for a live runtime | Handler is fixture-only or lacks a canonical production counterpart |

The last group includes fixture overview, adapter/provider routing, task/run
demo stores, review/quorum fixtures, snapshot/governance fixtures, benchmark
fixtures, settings, and synthetic memory-usage/security summaries. Returning a
fixture as live production state is prohibited.

Authentication, CSRF, CSP, security headers, and route-level authority checks
remain active. A fixture-only route does not become production-ready merely
because its frontend or unit tests exist.
