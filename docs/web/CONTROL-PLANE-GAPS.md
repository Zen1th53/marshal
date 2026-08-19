# MARSHAL Web Control Plane — Runtime Gap Inventory

**Audit Date:** 2026-08-19  
**Task:** T165  

This document tracks all identified runtime gaps, adapter requirements, and out-of-scope boundaries between the canonical Go runtime and the Web Control Plane frontend.

---

## 1. Identified Runtime Gaps & Planned Tasks

| Gap ID | Description | Current State | Required Target Component | Planned Task |
|---|---|---|---|:---:|
| **GAP-01** | Web Control API Router & Handlers | REST daemon exists for CLI, but lacks dedicated Web Control Plane routes | `internal/httpsrv/` dedicated Web API handler router | **T166** |
| **GAP-02** | Typed Web DTOs & JSON Serialization | Go domain models use internal structures with DB fields | Clean Web DTOs with UTC timestamps & string enums | **T167** |
| **GAP-03** | Server-Side Cookie Session Management | API currently uses Bearer tokens only | HttpOnly/SameSite/Secure cookie session store | **T173** |
| **GAP-04** | CSRF Token & Origin Validation | Mutations assume local CLI Unix socket | Double-submit CSRF token & Origin check | **T174** |
| **GAP-05** | Server-Sent Events (SSE) Hub | Realtime event subscription exists in-process | Web-compatible SSE streaming hub with `Last-Event-ID` | **T177** |
| **GAP-06** | Bounded Log Streaming Buffer | Raw logs read from disk without streaming window | Bounded ring buffer for realtime task log SSE | **T188** |

---

## 2. Explicitly Out-of-Scope Capabilities

The following features are strictly **PROHIBITED** from implementation in the Web Control Plane:
1. Direct Browser-to-SQLite DB connections (e.g. sql.js, WASM SQLite).
2. Remote interactive shell / arbitrary terminal execution inside the browser.
3. Arbitrary host filesystem explorer / file tree navigator.
4. Provider secret / API key text editor inside browser forms.
5. Third-party SaaS analytics trackers or external CDN script tags.
