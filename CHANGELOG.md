# Changelog

All notable changes to the MARSHAL project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-08-18

### Added
- **Complete MARSHAL TERRA v3 Execution Engine (55/55 Epics Completed)**:
  - **T14: Agent Auto-Recovery**: Automatic detection and state recovery for stalled agent executions (`internal/recovery`).
  - **T15: Model Router**: Capabilities-based LLM routing engine (`internal/router`).
  - **T16: Adversarial Cross-Model Verification**: Cross-model verification review pipeline (`internal/verify/crossmodel`).
  - **T17: Agent Tournament**: Multi-candidate execution arena across weighted performance/accuracy dimensions (`internal/tournament`).
  - **T18: Evolution Lab**: Genetic algorithm search engine for prompt strategy and policy optimization (`internal/evolution`).
  - **T27: Full MCP & A2A Runtime**: Production MCP (2026-07-28) and A2A (1.0) runtime session management (`internal/runtime/mcp_a2a`).
  - **T32: Verified Merge Queue**: Automated branch integration queue operating in isolated git worktrees (`internal/mergequeue`).
  - **T33: Deterministic Reconciliation 2.0**: State divergence recovery and reconciliation engine (`internal/reconciliation`).
  - **T34: Distributed MARSHAL**: Multi-node coordination and gossip state sync (`internal/distributed`).
  - **T35: Remote Worker Attestation**: Cryptographic attestation and hardware token verification (`internal/attestation`).
  - **T38: Explainable Scheduler**: Human-readable task scheduling decision explanations (`internal/explainable`).
  - **T40: Security Reputation**: Provider security reputation and threat tracking (`internal/reputation/security`).
  - **T42: MARSHAL TUI**: Terminal user interface for live task monitoring (`internal/tui`).
  - **T45: Research Agent & Evidence Pipeline**: Evidence collection and report synthesis (`internal/research`).
  - **T47: Controlled Self-Improvement**: Self-improvement recommendation engine with human approval guardrails (`internal/recommendation`).
  - **T51: marshal doctor 2.0**: Next-generation profile-aware system diagnostics (`internal/doctor2`).
  - **T52: Security Chaos & Conformance**: Fault injection and adversarial security scenario testing (`internal/chaos`).
  - **T53: Vibe Firewall**: Composite security evaluation firewall (`internal/vibefirewall`).
  - **T54: Evidence-Derived Trust Score**: Composite 0..100 trust scoring engine (`internal/trustscore`).
  - **T55: Trust Gate**: Final security gate enforcing evidence score minimums and officer vetoes (`internal/trustgate`).
- **CLI Enhancements**:
  - `marshal version` / `marshal --version` / `marshal --json version` subcommand outputting version (`v1.0.0`), commit SHA, build date, and database schema version (`v67`).
- **GitHub Release & Supply Chain Engineering**:
  - Reproducible release workflow (`.github/workflows/release.yml`) for Linux x86_64 and arm64 archives.
  - Automatic SHA-256 checksum generation (`checksums.txt`) and Software Bill of Materials (SBOM).
  - CodeQL static security analysis workflow (`.github/workflows/codeql.yml`).
  - Dependency review workflow (`.github/workflows/dependency-review.yml`).
  - Dependabot configuration (`.github/dependabot.yml`) for Go modules and GitHub Actions.

### Changed
- Updated SQLite database schema to **`v67`** with 67 fully migrated and indexed tables.
- Reconciled runtime version references across `internal/cli`, `internal/legal`, `RUNTIME-VERSION.yaml`, and documentation to `v1.0.0`.
- Overhauled `README.md` into a release-grade landing page with badges and Mermaid architecture diagram.

### Security
- Verified fail-closed `bubblewrap` mount isolation, mode `0600` Unix domain sockets, HMAC-backed Bearer tokens for MCP/A2A, and zero secret leakage in logs or committed files.


## Runtime 0.4.0 — 2026-08-12

Provider maturity and daily operations release:
- Implemented structured Provider Capability Data Model (`IMPLEMENTED`, `INSTALLED`, `AVAILABLE`, `AUTHENTICATED`, `CAPABILITY-PROBED`, `REAL-E2E-VERIFIED`).
- Enhanced `marshal doctor` with clean human-readable text output and `--probe-providers` / `--deep` capability probing.
- Added `marshal logs TASK-ID` command to inspect execution stdout/stderr artifacts, task events, and execution history.
- Added `marshal cancel TASK-ID` command to terminate active task executions cleanly.
- Added `--model` flag to `marshal run` to allow passing model overrides directly (e.g. local Ollama model selection).
- Augmented binary lookup in `project.FindBinary` to check `~/.local/bin` and `/usr/local/bin` in addition to `$PATH`.
- Increased API client HTTP timeout for `marshal run` from 35s to 15 minutes to prevent truncation during long LLM runs.
- Added automatic stale `.marshal/pid` file cleanup on daemon startup.
- Added `claude_real_test.go` integration test harness and fixed Gemini test runner duration units.

## Runtime 0.3.1 — 2026-08-12

Release integrity and bubblewrap sandbox storage patch release:
- Hardened bubblewrap sandbox storage mounts to allow adapter runtime directories (`/home/marshal/.codex`, `/home/marshal/.opencode`) to mount as `WritableTmpfs` while maintaining read-only binds for configuration files (`auth.json`, `config.toml`). This resolves read-only filesystem locks during SQLite database operations.
- Repaired OpenCode MCP full-chain E2E test protocol headers, tool invocation (`task_run`), and JSON-RPC response error assertions. Real execution duration (~22s for MCP, ~115s for A2A) confirmed calling OpenCode → local Ollama (`qwythos-9b`).
- Preserved `runtime-v0.3.0` tag immutably on commit `5f559cc`.

## Runtime 0.3.0 — 2026-08-12

Production-hardened local control plane with authenticated MCP & A2A protocols and real OpenCode/Ollama execution:
- High-entropy bearer token authentication (`marshal auth`) protecting MCP 2026-07-28 and A2A 1.0 HTTP endpoints with constant-time HMAC secret validation.
- Full OpenCode → local Ollama (`qwythos-9b`) execution path with bubblewrap isolation, `WritableTmpfs` support, and `XDG_CONFIG_HOME` / `OLLAMA_HOST` env forwarding.
- Comprehensive end-to-end integration test suite covering real Codex and real OpenCode adapters across native CLI, MCP 2026-07-28, and A2A 1.0 entry surfaces.
- Updated operational documentation and model compatibility matrix (`docs/providers/opencode-ollama.md`).

## Runtime 0.2.1 — 2026-08-12

Codex-first production runtime hardening and protocol standards compliance:
- Hardened real Codex execution worker and added semver-based standalone release selection.
- Modern stateless MCP 2026-07-28 request semantics, `Mcp-Method` / `Mcp-Name` header consistency validation, and `server/discover` endpoint.
- Standard A2A 1.0 HTTP+JSON binding (`GET /.well-known/agent-card.json`, `POST /message:send`, `A2A-Version: 1.0` header negotiation, ProtoJSON enums, and direct canonical task execution).
- Verified full-chain real Codex execution across native CLI, MCP 2026-07-28, and A2A 1.0 entry surfaces.

## Runtime 0.2.0 — 2026-08-11

Implemented executable multi-agent runtime and interoperability plane for MARSHAL:
- Executable adapters for Gemini CLI, Claude Code, and OpenCode alongside existing Codex adapter.
- Adapter probing, status, and command execution across native CLI surfaces (`marshal adapters`, `marshal adapter probe <name>`).
- Real MCP 2026-07-28 Runtime Server (`marshal mcp serve`, `marshal mcp status`) exposing runtime status, tasks, leases, policy checks, events, artifacts, and verification.
- Real A2A 1.0.0 Agent Server (`marshal a2a serve`, `marshal a2a status`) with Agent Card discovery, canonical task delegation mapping, and remote role spoofing prevention.
- Maintained strict MARSHAL policy enforcement, task-scoped worktrees, and bubblewrap isolation.

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

Added the executable runtime specification plane: marshal, canonical service, identity/heartbeat, policy enforcement, worker/sandbox, scheduler, event bus, secrets broker, artifact store, runtime schemas, health, threat model, and staged implementation roadmap.

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
