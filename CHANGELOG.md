# Changelog

## v1.5.0 — Autonomous Multi-Agent Collaborative Runtime & Frozen Core Verification

### Added

- **Frozen 6-Core Judgment Layer**:
  - **Core #1 (Epistemic Integrity & Claim Graph)**: Formal claim lifecycle (`UNSUPPORTED`, `SUPPORTED`, `VERIFIED`, `CONTESTED`, `STALE`, `INVALIDATED`), critical-claim gating, evidence-claim dependency DAG, and non-epistemic state rejection.
  - **Core #2 (Alignment Guard)**: Real worktree git diff inspector, blast radius calculation, deletion-as-satisfaction prevention, and semantic drift detection.
  - **Core #3 (Risk-Scaled Blind Interpretation)**: Independent multi-agent intent interpretation, semantic distance evaluation, and mandatory escalation upon divergence.
  - **Core #4 (Durable Handoff Checkpoints + Rollback)**: Transactional handoff checkpoints with state snapshots and one-click rollback to restore canonical state.
  - **Core #5 (Budget Contract + Termination)**: Multi-dimensional cost, wall-clock, turn, and token budget governance with hard termination and resource reclamation.
  - **Core #6 (Constraint Re-injection)**: Immutable constraint preservation, digest tracking, and active re-injection across 20+ turn handoff chains.
- **Terminal-First TUI Workspace**:
  - Live interactive TUI (`marshal tui` or default empty-arg project invocation) over canonical SQLite state with silence-by-default chatter reduction.
  - One-screen dashboard for Goal, active participants, claims, budget tracking, blocker management, and work ownership.
  - Operator controls for pause, resume, cancel, approval prompt resolution, checkpoint rollback, and inspectable routes.
- **Real Multi-Agent Collaboration Runtime**:
  - Fixed-role collaborative sessions (`architect`, `developer`, `qa`, `appsec`) across Claude CLI, OpenAI Codex, OpenCode, and Antigravity.
  - Peer discovery across process restarts, typed challenge/question protocols, and lease-governed work ownership.
- **First-Class Antigravity Harness**:
  - Dedicated `antigravity` adapter with automated capability probing, code-writing execution cells, and sandbox integration.
- **Harness Capability Intelligence & ULTRA Native Optimization**:
  - Probe-backed version-aware capability matrix (`adapters/MATRIX.json`) generating optimal execution routes, model selection, and reasoning effort without hallucinated flags.
- **Second-Wave Trust Hardening**:
  - Normalized failure fingerprinting, Review-the-Reviewer anti-rubber-stamping audits, bounded mutation testing, evidence bundle packaging, post-mortem cards, and ModelTaskTrust score gates.
- **SQLite Migrations 73–79**:
  - Forward migrations for goals, claims, checkpoints, budgets, blind interpretations, team sessions, and harness profiles.

See [`release/RELEASE_NOTES_1.5.0.md`](release/RELEASE_NOTES_1.5.0.md) for
detailed verification and release evidence.

## v1.0.1 — Canonical Community Consolidation and Production Hardening

### Fixed

- Reconciled the current memory runtime on schema v72, including governed
  consolidation, live task-memory refresh, retrieval quality gates, and
  provider-session import support.
- Added bounded Community Resource Awareness without adaptive Enterprise
  control behavior.
- Made clean initialization self-contained and symlink-safe.
- Prevented the production Web CLI from falling back to demo fixtures and
  disabled fixture-only API surfaces when a live runtime is attached.
- Made optional provider probes explicitly opt-in and reports them as
  `NOT_RUN` when not requested.
- Corrected runtime/pack version extraction in legal source evidence.

### Changed

- Consolidated current dependency and pinned GitHub Action updates.
- Synchronized README, installation, architecture, provider, memory, resource,
  Web boundary, security, and licensing/dependency documentation with the
  actual Community implementation.
- Added deterministic Linux amd64/arm64 release archives, SPDX SBOM generation,
  checksums, release-manifest verification, and clean-install validation to the
  existing release process.

See [`release/RELEASE_NOTES_1.0.1.md`](release/RELEASE_NOTES_1.0.1.md) for
exact verification and provider qualification results.

## Web Control Plane 1.0.0 (T165–T220) — 2026-08-20

Comprehensive Web Operator Control Plane:

### Added & Hardened Subsystems
- **Web Control API & Security Gateway (T165–T180)**:
  - Loopback-bound REST API gateway (`127.0.0.1:8787`) with one-time login token redemption, HttpOnly session cookies, double-submit CSRF, strict CSP (`default-src 'self'`), and correlation ID propagation.
  - Zero-secret egress filters and realtime SSE streaming engine with ring-buffer reconnection replay.
- **Mission Control, Tasks & DAGs (T181–T190)**:
  - System overview dashboard, agent inventory, task lifecycle manager with deterministic DAG visualization, CAS revision concurrency, idempotency deduplication, and safe log viewer.
- **Review, Quorum, Evidence & Provenance (T191–T199)**:
  - Multi-party quorum workspace, merge gate preflight, cryptographic evidence inspector, causal provenance "Why" trace, provider fleet inventory, security gate inspector, and global audit export.
- **Institutional Memory Explorer (T200–T207)**:
  - Hybrid lexical/vector retrieval explainability with RRF fusion, memory record lineage, governance conflict resolution workspace, working scratchpad inspector, snapshot diff/rollback, and read receipts influence tracing.
- **Operations, Diagnostics & Search (T208–T215)**:
  - Doctor diagnostic center, SQLite state backup creation/verification/restore, GC retention maintenance jobs, empirical benchmarks dashboard, release trust SBOM viewer, capability-aware settings, and global entity search.
- **Packaging, Performance & Conformance (T216–T220)**:
  - Strict keyboard accessibility pass, p50/p95 latency benchmarking, zero-Node standalone Go `embed.FS` production asset serving, end-to-end adversarial security test suite, and release manifest parity.

## Runtime 1.0.0 (Hardened) — 2026-08-19

Comprehensive Core Hardening release (T56–T76):

### Added & Hardened Subsystems
- **Remote Control-Plane Security (T56–T59)**:
  - Insecure remote bind rejection: MCP and A2A servers strictly enforce loopback binding (`127.0.0.1`, `::1`, `localhost`). Non-loopback addresses return `ErrInsecureRemoteBind`.
  - Fine-grained token capabilities: Bearer tokens now enforce explicit scopes (`task:run`, `task:claim`, `mcp:read`, `a2a:send`).
  - Constant-time HMAC authentication, rate limiting, and sensitive credential redaction across all protocol boundaries.
  - A2A role spoofing prevention: remote agents cannot self-assign internal administrative or orchestrator roles.
- **Runtime Security Composition (T60–T63)**:
  - Execution cell composition boundary and fail-closed permission checks.
  - Strict policy enforcement requiring explicit active allow (`Allow: true`).
  - Network namespace isolation and explicit task-level network access requirements.
  - Real resource governance: CPU, RAM, and PID quotas, 500MB worktree disk budget, and process group escalation timeouts.
- **Real Orchestration Engine (T64–T66)**:
  - Multi-factor scheduler scoring engine (success rate 0.40, load 0.30, context util 0.20, cost 0.10) with deterministic tie-breaking.
  - Dynamic model profile routing scoring context headroom, latency class, cost class, locality preference, and provider pinning.
  - Recovery state machine with failure classification, poisoned checkpoint quarantine, and exponential backoff.
- **Task Lifecycle & Verification Quorum (T67–T68)**:
  - Canonical Review -> QA -> Security -> Merge task lifecycle with role authorization and HeadCommit binding.
  - Quorum verification merge gate requiring multi-party signed attestations (QA + AppSec) with veto blocking.
- **Lifecycle Hygiene & Storage Safety (T69–T73)**:
  - Worktree lifecycle retention and garbage collection CLI (`marshal gc worktrees`).
  - Content-addressed artifact reference tracking and garbage collection CLI (`marshal gc artifacts`).
  - SQLite online backup, restore preflight, and integrity verification workflow (`marshal state backup`, `verify-backup`, `restore`).
  - Startup reconciliation recovering dead worker runs, stale sessions, and expired leases.
  - SQLite concurrency and load profile hardening under heavy read/write contention.


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
