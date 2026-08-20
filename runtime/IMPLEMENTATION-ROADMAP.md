# Runtime Implementation Roadmap

## Milestone 1 — Local Control Plane
Status: Fully implemented in runtime `1.0.0` (SQLite schema `v69`).
- Local daemon over mode-0600 Unix socket.
- SQLite WAL storage with transactions, foreign keys, and migration engine.
- Task DAG, atomic leases, and session identity registry.
- Policy engine with capability broker and contextual approvals.
- Git worktree lifecycle management and content-addressed artifact store.

## Milestone 2 — Worker & Execution Subsystem
Status: Fully implemented in runtime `1.0.0`.
- Bubblewrap sandbox engine with read-only rootfs and tmpfs mounts.
- Provider adapters for Codex, OpenCode (local Ollama), Gemini CLI, and Claude Code.
- Resource governance: CPU, memory, PID limits, and 500MB worktree disk budget.
- Worker process group termination and timeout enforcement.

## Milestone 3 — Orchestration & Recovery Engine
Status: Fully implemented in runtime `1.0.0`.
- Multi-factor scheduler scoring (success rate, load, context, cost).
- Dynamic model profile routing with latency, cost, and locality weighting.
- Recovery state machine with failure classification and backoff.
- Startup reconciliation recovering dead runs, stale sessions, and expired leases.

## Milestone 4 — Verification, Lifecycle & Quorum Merge Gate
Status: Fully implemented in runtime `1.0.0`.
- Canonical Review -> QA -> Security -> Merge task lifecycle.
- Quorum verification engine requiring multi-party attestations (QA + AppSec).
- Append-only audit logging and provenance chain generation.

## Milestone 5 — Protocols & Remote Control Plane
Status: Fully implemented for local operations in runtime `1.0.0`.
- Loopback-bound, capability-token authorized MCP (`2026-07-28`) server.
- Loopback-bound, role-protected A2A (`1.0`) agent server.
- Fail-closed remote bind protection (`ErrInsecureRemoteBind`).

## Future Milestones
- Distributed multi-host worker clustering.
- Remote object storage backends (S3/GCS) for artifact distribution.
- Hardware security module (HSM) secrets brokering.
