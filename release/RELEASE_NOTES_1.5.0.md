# MARSHAL v1.5.0 — Autonomous Multi-Agent Collaborative Runtime & Frozen Core Verification

MARSHAL v1.5.0 introduces a terminal-first collaborative workspace, the frozen six-Core
judgment layer, real multi-agent team sessions with fixed roles, first-class Google Antigravity
integration, version-aware Harness Capability Intelligence with ULTRA native optimization,
and second-wave trust hardening.

## Highlights

- **Frozen 6-Core Judgment Layer**:
  - **Epistemic Ledger & Claim Graph (Core #1)**: Epistemic state machine (`UNSUPPORTED`, `SUPPORTED`, `VERIFIED`, `CONTESTED`, `STALE`, `INVALIDATED`), critical-claim gating, and directed evidence dependencies.
  - **Alignment Guard (Core #2)**: Deep worktree git diff analysis, blast radius calculation, and semantic drift / deletion-as-satisfaction prevention.
  - **Risk-Scaled Blind Interpretation (Core #3)**: Independent parallel interpretation of user intent across high-risk tasks, semantic divergence scoring, and mandatory human escalation.
  - **Durable Handoff Checkpoints & Rollback (Core #4)**: Transactional state snapshots across turn handoffs with atomic rollback capabilities to restore canonical state.
  - **Budget & Termination Contract (Core #5)**: Multi-dimensional cost, wall-clock, turn, and token budget governance with hard termination and execution reclamation.
  - **Constraint Re-injection (Core #6)**: Immutable constraint preservation, digest validation, and active re-injection across 20+ turn handoff chains.
  - Strictly no 7th core added.
- **Terminal TUI Workspace**:
  - Live terminal-first collaborative workspace (`marshal tui` or default empty-arg project launch) over canonical SQLite state with silence-by-default chatter reduction.
  - Real-time dashboard displaying Goal revision, active participants, claims, budget tracking, blocker management, and work ownership.
  - Interactive operator controls for pause/resume/cancel, approval decision prompts, checkpoint rollback, and inspectable routes.
  - Interactive commands: `/status`, `/goal`, `/mode`, `/agents`, `/claims`, `/inspect`, `/evidence`, `/why`, `/route`, `/msg`, `/handoff`, `/approve`, `/reject`, `/checkpoint`, `/rollback`, `/budget`, `/pause`, `/resume`, `/cancel`, `/help`, `/quit`. Each is dispatched by `internal/tui/commands.go` and covered by tests in `internal/tui/`.
- **Real Multi-Agent Collaboration**:
  - Fixed-role collaborative sessions across Claude CLI (Architect), OpenAI Codex (Core Developer), OpenCode (QA/Verifier), and Google Antigravity (Integration Developer).
  - Cross-restart peer discovery, typed agent-to-agent challenge/question/handoff path, and lease-governed work ownership.
- **First-Class Antigravity Harness Adapter**:
  - Dedicated `antigravity` adapter with automated capability probing, code-writing execution cells, and sandbox integration.
- **Harness Capability Intelligence & ULTRA Native Optimization**:
  - Probe-backed version-aware capability matrix (`adapters/MATRIX.json`) generating optimal execution routes, model selection, and reasoning effort without hallucinated flags.
- **Second-Wave Trust Hardening**:
  - Normalized failure fingerprinting, Review-the-Reviewer anti-rubber-stamping audits, bounded mutation testing, evidence bundle packaging, post-mortem cards, and ModelTaskTrust score gates.
- **Canonical SQLite Schema v79**:
  - Seven forward migrations (73 through 79) providing durable persistence for goals, claims, checkpoints, budgets, blind interpretations, collaborative team sessions, and harness profiles.

## Verification

The mandatory Community release gate ran on 2026-09-05 on Linux x86_64:

| Gate | Result |
|---|---|
| Go build / vet / test / conformance | PASS |
| Web `npm run test:run` (51 test suites, 118 tests) | PASS |
| Web `npm run build` (typecheck + vite bundle) | PASS |
| 13 Heterogeneous E2E Scenarios (`v15_release_e2e_test.go`) | PASS |
| Clean install, initialization, doctor, daemon, first local workflow, backup, restore | PASS |
| Python pack conformance and release trust tools (`test_build_release`, `test_clean_install`, `test_release_trust`) | PASS |
| Source pack manifest verification (`tools/release_verify.py`) | PASS |
| Reproducible archives, SPDX SBOM, checksums, and release-manifest generation | PASS |

## Provider Verification

| Provider path | Adapter / probe | Adapter / Model Integration | Canonical Runtime Live Network E2E |
|---|---|---|---|
| Claude Code | PASS — adapter & CLI probe | PASS — simulation & contract | NOT_RUN — credentialed live API not enabled |
| OpenAI Codex | PASS — adapter & CLI probe | PASS — simulation & contract | NOT_RUN — credentialed live API not enabled |
| OpenCode | PASS — adapter & CLI probe | PASS — simulation & contract | NOT_RUN — local Ollama daemon not running |
| Antigravity | PASS — adapter & IDE probe | PASS — code-writing cell execution | PASS — local subprocess cell execution |
| Gemini CLI | NOT_AVAILABLE — optional binary absent | NOT_RUN | NOT_RUN |

In accordance with MARSHAL's Epistemic Rule, unrun live external network integrations are strictly classified as `NOT_RUN` rather than fabricated passes.

## Known Limitations & Rollback Plan

- **External Network Enforcement**: Fine-grained endpoint filtering requires upstream egress allowlisting; sandbox enforcement is fail-closed by default.
- **Host Sandbox**: Bubblewrap is the primary Linux isolation engine; unprivileged namespace support is required on the host system.
- **Second-Wave Gates**: High-trust automation rules require a minimum of 10 verifiable historical task executions before autonomy gates unlock.
- **Rollback Procedure**:
  - Database rollback: Upgrading from v72 creates a pre-migration backup (`.marshal/backups/`). To revert, stop the daemon, restore the v72 backup database, and replace the MARSHAL binary with v1.0.1.
  - Checkpoint rollback: Within a running session, execute `rollback <checkpoint_id>` in the TUI or CLI to restore the exact pre-handoff state.
