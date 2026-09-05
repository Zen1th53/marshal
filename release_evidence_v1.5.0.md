# MARSHAL v1.5.0 Release Evidence Report

**Release Date:** 2026-09-05  
**Release Version:** v1.5.0  
**Target Schema:** SQLite Schema v79  
**Pack Version:** 6.0.0  
**Architecture:** Linux x86_64, arm64  
**Epistemic Rule:** "Agents produce claims. MARSHAL produces evidence. Never claim PASS, VERIFIED, DONE or SUCCESS without the required current-state evidence."

---

## 1. Executive Summary

MARSHAL v1.5.0 successfully delivers:
1. **The Frozen Six-Core Runtime**:
   - Core #1: Epistemic Integrity & Claim Graph (`internal/epistemic/`, `internal/model/claim.go`, migration 74)
   - Core #2: Alignment Guard (`internal/alignment/`)
   - Core #3: Risk-Scaled Blind Interpretation (`internal/interpretation/`, `internal/model/interpretation.go`, migration 77)
   - Core #4: Durable Handoff Checkpoints + Rollback (`internal/memory/checkpoint/`, `internal/store/checkpoint.go`, migration 75)
   - Core #5: Budget Contract + Hard Termination (`internal/budget/`, `internal/model/budget.go`, migration 76)
   - Core #6: Constraint Re-injection (`internal/reinjection/`, `protocol.Handoff.ConstraintRefs`)
   - *Verification*: Exactly 6 cores; no seventh core was added.
2. **Terminal TUI Workspace**:
   - Interactive control plane (`internal/tui/`, `internal/cli/tui_cli.go`) with silence-by-default chatter reduction, live dashboard, inspector, and approval prompt handlers.
3. **Real Multi-Agent Collaboration**:
   - Fixed-role collaborative runtime (`internal/collaboration/`, `internal/model/collaboration.go`, migration 78) across Claude CLI, OpenAI Codex, OpenCode, and Antigravity.
4. **Harness Capability Intelligence & ULTRA Native Optimization**:
   - Version-aware capability matrix (`adapters/MATRIX.json`, `internal/harness/`, migration 79) computing optimal native flags, reasoning effort, and model routing.
5. **First-Class Google Antigravity Integration**:
   - Fully implemented adapter (`internal/adapter/antigravity/`, `adapters/antigravity/ADAPTER.md`) with automated IDE/CLI probing and code-writing execution cells.
6. **Second-Wave Trust Hardening**:
   - Normalized failure fingerprinting (`internal/epistemic/fingerprint.go`), Review-the-Reviewer audits (`internal/epistemic/review.go`), bounded mutation testing, evidence bundle packaging (`internal/bundle/evidence_bundle.go`), and post-mortem cards.

---

## 2. Gate Verification Evidence

| Gate ID | Description | Tool / Command | Evidence / Result | Status |
|---|---|---|---|---|
| GATE-GO-VET | Static analysis & linting | `go vet ./...` | Clean exit (code 0, zero warnings) | **PASS** |
| GATE-GO-TEST | Core Go test suites | `go test ./...` | All packages passed | **PASS** |
| GATE-GO-STORE | Store concurrency & SQLite retry | `go test ./internal/store/...` | Contention and lock retry passed (15.26s) | **PASS** |
| GATE-E2E-13 | 13 Heterogeneous Scenarios | `go test -v -run TestE2E_ ./internal/integration/...` | 13/13 scenarios passed | **PASS** |
| GATE-WEB-TEST | Web control plane test suite | `npm --prefix web run test:run` | 51 test suites, 118 tests passed | **PASS** |
| GATE-WEB-BUILD | Web control plane build | `npm --prefix web run build` | TypeScript checked, Vite built in 518ms | **PASS** |
| GATE-PY-TOOLS | Release trust & install smoke | `python3 -m unittest discover tools/tests` | 8 tests passed (reproducible build, SBOM, install) | **PASS** |
| GATE-MANIFEST | Pack manifest verification | `python3 tools/release_verify.py . distribution/PACK-MANIFEST.json` | Status PASS, zero errors | **PASS** |
| GATE-CLEAN-INSTALL | Clean install from binary | `test_clean_install.py` | Init, doctor, daemon, backup, restore verified | **PASS** |

---

## 3. Heterogeneous E2E Scenarios (Phase 14)

All 13 scenarios implemented in `internal/integration/v15_release_e2e_test.go` executed and passed:

1. **TestE2E_01_ReadOnlyTeamDiscovery**: Read-only team discovery and session coordination across Claude, Codex, OpenCode, and Antigravity.
2. **TestE2E_02_ImplementationAndHandoff**: Active implementation handoff carrying changed files, diff references, and claims.
3. **TestE2E_03_ClaimDisagreementAndJustifiedRevision**: Claim disagreement where invalid evidence is contested and justified revision prevails.
4. **TestE2E_04_ConstraintPersistenceAcross20Turns**: Strict constraint digest and body preservation across a 20-turn handoff chain.
5. **TestE2E_05_MissingCriticalClaimDeniesSuccess**: Verification gate fails closed and blocks SUCCESS when a critical claim is missing or unverified.
6. **TestE2E_06_DeletionAsSatisfactionBlocked**: Alignment guard detects and blocks deletion-as-satisfaction (e.g. deleting tests to pass verification).
7. **TestE2E_07_CheckpointRestartRollback**: Checkpoint state snapshot capture, process restart recovery, and transactional rollback.
8. **TestE2E_08_BudgetExhaustionTermination**: Hard termination and execution cell reclamation upon cost/turn budget exhaustion.
9. **TestE2E_09_BlindInterpretationDivergence**: Parallel intent interpretation detecting semantic drift and escalating to human operator.
10. **TestE2E_10_HarnessNativeOptimizationRoute**: ULTRA router selects optimal native flags and reasoning effort based on installed version probe.
11. **TestE2E_11_AntigravityFirstClassExecution**: Antigravity harness executes code writing in an isolated cell and produces verified evidence.
12. **TestE2E_12_ProviderVersionDriftFallback**: Harness intelligence detects version drift and falls back safely to baseline configuration.
13. **TestE2E_13_TUIRuntimeSessionLifecycle**: TUI workspace boots, processes `/status`, `/claims`, `/msg`, and survives reload.

---

## 4. Provider Qualification & Network Tests

| Provider | Probe Status | Model / Contract Integration | Live External Network Test |
|---|---|---|---|
| Claude Code | PASS (Local CLI detected) | PASS | NOT_RUN (Requires live authenticated API key) |
| OpenAI Codex | PASS (Local CLI detected) | PASS | NOT_RUN (Requires live authenticated API key) |
| OpenCode | PASS (Local binary detected) | PASS | NOT_RUN (Local Ollama daemon not running) |
| Antigravity | PASS (Host environment detected) | PASS | PASS (Subprocess execution cell) |
| Gemini CLI | NOT_AVAILABLE (Binary not on PATH) | NOT_RUN | NOT_RUN |

---

## 5. Known Limitations & Rollback

- **Network Sandboxing**: Bubblewrap enforces strict `--unshare-net` isolation. Fine-grained host allowlisting requires upstream proxy configuration.
- **Autonomy Gates**: ModelTaskTrust scores require >= 10 verified historical tasks before full automated review bypass is granted.
- **Rollback Procedure**:
  - Roll back to v1.0.1: Restore SQLite backup from `.marshal/backups/`, switch binary to v1.0.1.
  - Roll back session: Use `rollback <checkpoint-id>` via TUI or CLI.
