# Changelog

## Runtime 0.2.0 — 2026-08-11

Implemented executable multi-agent runtime and interoperability plane for SLAVES:
- Executable adapters for Gemini CLI, Claude Code, and OpenCode alongside existing Codex adapter.
- Adapter probing, status, and command execution across native CLI surfaces (`slaves adapters`, `slaves adapter probe <name>`).
- Real MCP 2026-07-28 Runtime Server (`slaves mcp serve`, `slaves mcp status`) exposing runtime status, tasks, leases, policy checks, events, artifacts, and verification.
- Real A2A 1.0.0 Agent Server (`slaves a2a serve`, `slaves a2a status`) with Agent Card discovery, canonical task delegation mapping, and remote role spoofing prevention.
- Maintained strict SLAVES policy enforcement, task-scoped worktrees, and bubblewrap isolation.

## 6.0.0 — 2026-08-11

Added A2A/MCP protocol interoperability profiles, JSON Schema 2020-12 contracts,
OpenTelemetry-oriented telemetry, release provenance/signing infrastructure,
live behavioral conformance, fault injection, plugin compatibility, multi-tenant
governance, and reproducible-packaging rules.

Owner signing remains explicitly external; this generated pack is not falsely
marked as owner-signed.

## 5.0.0 — 2026-08-11

Added universal native-agent adapters, executable static conformance checks,
18 adversarial behavioral scenarios, project detection/state reconciliation,
project bootstrap, capability-based model routing, and safe pack distribution /
self-upgrade contracts.

Adapters are verified against dated official/upstream documentation but installed
versions must still be probed.

## 4.0.0 — 2026-08-11

Added the executable runtime specification plane: agentctl, canonical service, identity/heartbeat, policy enforcement, worker/sandbox, scheduler, event bus, secrets broker, artifact store, runtime schemas, health, threat model, and staged implementation roadmap.

This is a runtime specification, not a production implementation claim.

## 3.0.0 — 2026-08-11

Added the remaining protocol-first control-plane layers:

- capability and tool permission policy,
- instruction-trust / prompt-injection boundary,
- environment bootstrap and reproducibility,
- ownership/review routing,
- end-to-end traceability,
- artifact provenance,
- CI/CD orchestration,
- durable dependency/supply-chain ledger,
- run observability/audit,
- data classification/retention,
- resource budgeting,
- liveness/deadlock control,
- pack versioning/migrations,
- backup/restore/disaster recovery.

No mandatory external runtime service was introduced.

## 2.x

Previous generation added:
- task graph/leases,
- worktree isolation,
- context routing,
- shared memory backend contract,
- approvals,
- doctrine evaluations.

## 1.x

Initial role/doctrine/memory generations.
