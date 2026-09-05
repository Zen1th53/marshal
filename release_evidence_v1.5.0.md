# MARSHAL v1.5.0 Release Evidence Report

**Release Date:** 2026-09-05  
**Release Version:** v1.5.0  
**Evidence Commit:** `cf2dd2b` — every gate below was executed against this exact tree. Results from earlier commits are not carried forward.  
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
| GATE-GO-STORE | Store concurrency & SQLite retry | `go test ./internal/store/...` | Contention and lock retry passed | **PASS** |
| GATE-E2E-13 | 13 Heterogeneous Scenarios | `go test -v -run TestE2E_ ./internal/integration/...` | 13/13 scenarios passed | **PASS** |
| GATE-WEB-TEST | Web control plane test suite | `npm --prefix web run test:run` | 51 test suites, 118 tests passed | **PASS** |
| GATE-WEB-BUILD | Web control plane build | `npm --prefix web run build` | TypeScript checked, Vite bundle emitted | **PASS** |
| GATE-PY-TOOLS | Release trust & install smoke | `python3 -m unittest discover tools/tests` | 8 tests passed (reproducible build, SBOM, install) | **PASS** |
| GATE-MANIFEST | Pack manifest verification | `python3 tools/release_verify.py . distribution/PACK-MANIFEST.json` | Status PASS, zero errors | **PASS** |
| GATE-TUI-LIVE | Interactive TUI command surface | `marshal tui` driven from a real binary against a live SQLite project | 17 commands dispatched, zero unknown-command responses; approval decisions verified in SQLite | **PASS** |
| GATE-CLEAN-INSTALL | Clean install from binary | `test_clean_install.py` | Covered by GATE-PY-TOOLS (init, doctor, daemon, backup, restore) | **PASS** |

### Gate Correction Notice

An earlier revision of this report recorded GATE-GO-TEST and GATE-MANIFEST as
PASS for commit `7052197`. Both were re-run against that exact commit and both
failed: the embedded `internal/app/defaults/RUNTIME-VERSION.yaml` was still at
v1.0.1, and `distribution/PACK-MANIFEST.json` carried a stale hash plus two
unlisted release documents. The defects were fixed in `b984204` and `e528797`
respectively, and the table above reflects re-execution against `cf2dd2b`. A
gate result is only valid for the commit it was executed against.

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
13. **TestE2E_13_TUIRuntimeSessionLifecycle**: TUI workspace boots, processes `/goal`, `/mode`, `/status`, `/claims`, `/route` and `/msg`, asserts that no documented command falls through to the unknown-command branch, and survives reload.

---

## 4. Provider Qualification & Network Tests

Probes were executed with `marshal adapter probe <name>` against the release
binary. "Standalone live" means the provider CLI was invoked directly and
returned a correct completion. "Live through MARSHAL" means a task was executed
end to end via `marshal run`.

| Provider | Probe Status | Version Observed | Standalone Live | Live through MARSHAL |
|---|---|---|---|---|
| Claude Code | PASS (local CLI detected) | 2.1.261 | PASS | BLOCKED (sandbox egress) |
| OpenAI Codex | PASS (local CLI detected) | codex-cli 0.149.1 | PASS | BLOCKED (sandbox egress) |
| OpenCode | PASS (local binary detected) | 1.18.22 | PASS | BLOCKED (sandbox egress) |
| Antigravity | PASS (host IDE detected) | IDE 2.0.11; `agy` CLI absent | NOT_RUN | NOT_RUN |
| Gemini CLI | NOT_AVAILABLE (binary not on PATH) | — | NOT_RUN | NOT_RUN |

**BLOCKED (sandbox egress).** `marshal run` executes each provider inside a
bubblewrap sandbox. Because MARSHAL cannot enforce per-endpoint egress rules,
`egressEnforcementAvailable()` returns false and the sandbox is constructed with
`--unshare-net`, denying network entirely rather than silently broadening to
unrestricted host networking. A provider that needs API access therefore blocks
inside the cell. This was observed directly: MARSHAL created the isolated
worktree, launched a real sandboxed `codex exec` process, and the task remained
in `working` until cancelled. This is the documented fail-closed design, not a
regression, and `marshal cancel` reclaimed the task correctly. Live
heterogeneous collaboration through `marshal run` therefore remains **BLOCKED**
in this environment and is not claimed as PASS.

**Antigravity NOT_RUN.** `adapters/antigravity/ADAPTER.md` specifies the `agy`
CLI with `agy exec` headless mode. `agy` is not installed; the binary on PATH
resolves to the Antigravity Electron IDE (v2.0.11), which opens a desktop
window rather than serving headless execution. The previous revision of this
report recorded Antigravity's live external network test as PASS. That claim is
withdrawn: no headless Antigravity execution was performed.

---

## 4a. TUI Command Surface Verification

Executed by driving the real `marshal tui` binary built from the evidence commit
against a live SQLite project, with two pending approval records seeded into the
canonical `approvals` table.

| Command | Observed Behaviour | Status |
|---|---|---|
| `/status` | Rendered project, session, goal, understanding, termination, claim counts, team, budget, pending approvals; counts changed as underlying state changed | **PASS** |
| `/goal` | Created and read back GoalContract revision 1 | **PASS** |
| `/agents` | Listed 4 fixed-role participants with harness and model | **PASS** |
| `/claims` | Reported claim set for the active goal | **PASS** |
| `/inspect` | Resolved checkpoint and approval records; inferred record kind; reported absence for an unknown id | **PASS** |
| `/evidence` | Resolved evidence against claim ledger | **PASS** |
| `/why` | Reported ULTRA routing explanation | **PASS** |
| `/route` | Recomputed plans through the ULTRA router: `role=qa` selected opencode/deepseek-coder, `role=appsec risk=R3` selected antigravity/gemini-2.5-pro | **PASS** |
| `/msg` | Persisted an operator message to the collaborative session | **PASS** |
| `/approve` | With 2 pending, refused to guess and listed both; `/approve APR-1` granted with expiry, revision 1 | **PASS** |
| `/reject` | `/reject APR-2` denied and retained the record without expiry, revision 1 | **PASS** |
| `/checkpoint` | Created a durable handoff checkpoint | **PASS** |
| `/rollback` | Restored from a persisted checkpoint by id | **PASS** |
| `/budget` | Reported token, cost, call, handoff and wall-clock consumption | **PASS** |
| `/pause`, `/resume` | Transitioned collaborative session status | **PASS** |
| `/cancel` | Recorded CANCELLED termination, reflected by a subsequent `/status` | **PASS** |

Zero commands returned `Unknown command`. Approval outcomes were confirmed
against SQLite ground truth rather than terminal output alone:

```
('APR-1', 'approved', 'operator', '2026-09-05T18:59:16.82440633Z', 1)
('APR-2', 'denied',   'operator', None,                            1)
```

Both `marshal tui` and the default empty-argument project invocation were
launched from the release binary and rendered the workspace.

---

## 5. Known Limitations & Rollback

- **Network Sandboxing**: Bubblewrap enforces strict `--unshare-net` isolation. Fine-grained host allowlisting requires upstream proxy configuration. Consequence: a provider requiring API egress cannot complete a `marshal run` execution cell until an egress-filtering proxy is wired. This is fail-closed by design (see `egressEnforcementAvailable`), and it is why live provider execution is recorded as BLOCKED rather than PASS in section 4.
- **Autonomy Gates**: ModelTaskTrust scores require >= 10 verified historical tasks before full automated review bypass is granted.
- **Rollback Procedure**:
  - Roll back to v1.0.1: Restore SQLite backup from `.marshal/backups/`, switch binary to v1.0.1.
  - Roll back session: Use `rollback <checkpoint-id>` via TUI or CLI.
