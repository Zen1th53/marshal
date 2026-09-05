# Runtime implementation status

## Implemented in Community v1.5.0

- local daemon over a mode-`0600` Unix socket;
- SQLite WAL state with forward migrations through schema v79;
- frozen 6-Core judgment layer (Epistemic Ledger/Claim Graph, Alignment Guard, Blind Interpretation, Checkpoints/Rollback, Budget/Termination, Constraint Re-injection);
- terminal-first collaborative TUI workspace (`marshal tui` / default empty-arg project launch);
- fixed-role multi-agent team sessions (Claude, Codex, OpenCode, Antigravity) with typed handoffs and peer discovery;
- harness capability intelligence (`adapters/MATRIX.json`) with version-aware ULTRA native optimization routing;
- first-class Antigravity harness adapter with automated probing and code-writing execution;
- second-wave trust hardening (failure fingerprinting, Review-the-Reviewer, mutation testing, evidence bundles, post-mortems);
- task/agent/session/lease coordination and startup reconciliation;
- role, capability, policy, risk, and verification gates;
- Git worktrees, Bubblewrap execution cells, timeouts, and bounded artifacts;
- Codex, OpenCode, Gemini, Claude, and Antigravity runtime adapters;
- canonical evidence-linked memory recall/capture, governance, and receipts;
- loopback MCP (`2026-07-28`), A2A (`1.0`), and authenticated Web entry points;
- backup creation, verification, and offline recovery; and
- read-only Community Resource Awareness.

## Explicitly not Community v1.5.0

- adaptive resource governors or aggressive/performance modes;
- continuous dynamic concurrency, context, provider, or model tuning;
- fleet-wide resource intelligence and cross-worker placement;
- distributed multi-host coordination; and
- remote object-store backends.

Provider adapters are implemented and validated via rigorous integration tests, but authenticated live external-provider network tests remain NOT_RUN when host environment credentials/binaries are not configured.
