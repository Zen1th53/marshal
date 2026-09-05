# Runtime plane

This directory contains the executable control-plane contracts for MARSHAL
v1.5.0. The current database is SQLite schema v79.

## Components

- `MARSHAL-CLI.md` — CLI contract
- `MEMORY-SERVICE.md` — canonical memory contract
- `IDENTITY-REGISTRY.md` — agent/session identity and heartbeat
- `POLICY-ENGINE.md` — authorization and capability broker
- `SANDBOX.md` — isolated worker execution
- `SCHEDULER.md` — task dispatch and leases
- `EVENT-BUS.md` — durable event semantics
- `SECRETS-BROKER.md` — scoped secrets and redaction
- `ARTIFACT-STORE.md` — content-addressed artifacts
- `WORKER-PROTOCOL.md` — worker lifecycle
- `HEALTH.md` — readiness diagnostics
- `THREAT-MODEL.md` — trust boundaries

```text
TUI / CLI / MCP / A2A / supported Web routes
                  |
                  v
              app.Runtime
                  |
 capability → policy → network → sandbox → provider
                  |
  claims → alignment → budget → handoffs → checkpoints
                  |
           evidence → memory → SQLite (v79)
```

The runtime uses task-scoped Git worktrees, bounded worker processes,
Bubblewrap when required, sanitized evidence, and SQLite transactions. Missing
required isolation fails closed. Process-only fallback is disabled by default
and limited to explicitly opted-in R0/R1 work.

Community Resource Awareness is read-only and advisory. It does not implement
adaptive resource control, fleet placement, or automatic provider/model
routing. Multi-host clustering and remote artifact stores remain outside the
v1.5.0 Community runtime.
