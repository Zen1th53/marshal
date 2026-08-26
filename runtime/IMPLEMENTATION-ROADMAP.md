# Runtime implementation status

## Implemented in Community v1.0.1

- local daemon over a mode-`0600` Unix socket;
- SQLite WAL state with forward migrations through schema v72;
- task/agent/session/lease coordination and startup reconciliation;
- role, capability, policy, risk, and verification gates;
- Git worktrees, Bubblewrap execution cells, timeouts, and bounded artifacts;
- Codex, OpenCode, Gemini, and Claude runtime adapters;
- canonical evidence-linked memory recall/capture, governance, and receipts;
- loopback MCP (`2026-07-28`), A2A (`1.0`), and authenticated Web entry points;
- backup creation, verification, and offline recovery; and
- read-only Community Resource Awareness.

## Explicitly not Community v1.0.1

- adaptive resource governors or aggressive/performance modes;
- continuous dynamic concurrency, context, provider, or model tuning;
- fleet-wide resource intelligence and cross-worker placement;
- distributed multi-host coordination; and
- remote object-store backends.

Provider adapters are implemented, but authenticated external-provider E2E was
NOT_RUN for the v1.0.1 release unless the release notes explicitly record a
successful opt-in gate.
