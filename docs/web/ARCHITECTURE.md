# MARSHAL Web Control Plane — Technical Architecture & Invariants (T165–T220)

This document describes the end-to-end architecture, security boundaries, and runtime invariants of the MARSHAL Web Control Plane.

---

## 1. System Topology & Data Flow

```
[ Operator Browser (React 19 + TypeScript + Graphite DS) ]
                        │
                        ▼  HTTP/1.1 Loopback (127.0.0.1:8787)
[ Web Control API Gateway (internal/webcontrol) ]
    ├─ Security Middleware (CSRF, Strict CSP, Nosniff, RateLimit)
    ├─ Ephemeral Session Store (One-time code redemption -> HttpOnly Cookie)
    ├─ SSE Event Hub (Realtime state event broadcast)
    └─ Static Asset Handler (embed.FS with SPA Fallback)
                        │
                        ▼  Direct In-Process Invocations
[ MARSHAL Canonical Core Runtime & Subsystems ]
    ├─ Task Scheduler & DAG Engine
    ├─ Multi-Agent Quorum & Review Gate
    ├─ Memory Subsystem (Adaptive, Lexical, Vector, Governance)
    ├─ Sandbox & Execution Boundary (Bubblewrap Rootless)
    └─ SQLite WAL Storage Engine
```

---

## 2. Fundamental Security & Operational Invariants

1. **Zero Direct-Store Access**:
   - The browser frontend client interacts exclusively with the Web Control Plane HTTP and SSE APIs.
   - The browser never opens SQLite, never runs a duplicate local database, and never bypasses runtime security gates.

2. **Zero Browser Secrets**:
   - No credentials, API tokens, master passwords, or raw encryption keys are stored in `localStorage` or `sessionStorage`.
   - Authentication relies strictly on CLI-generated one-time codes and HttpOnly session cookies.

3. **Atomic Canonical Mutations**:
   - Every mutation (task claiming, task creation, DAG execution, quorum voting, merge authorization, memory mutation, and snapshot rollback) executes through the canonical MARSHAL runtime with CAS revision bounds and idempotency keys.

4. **Standalone Zero-Node Production Distribution**:
   - Production builds compile the Vite frontend into hashed static assets and embed them into the Go binary via `embed.FS`.
   - The daemon serves the full web control plane without requiring Node.js or any external internet access.
