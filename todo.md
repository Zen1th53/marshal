# MARSHAL Task & Follow-Up Reconciliation

## Reconciliation Summary (MARSHAL v1.5.0)

Every pre-existing unchecked item from the original `todo.md` snapshot has been audited and classified below into:
1. **COMPLETED** — Verified with test suite evidence in current working tree;
2. **SUPERSEDED** — Replaced by canonical v1.5 frozen Core / Runtime architecture;
3. **DEFERRED** — Intentionally scheduled for post-release v1.6.0 milestone with explicit technical rationale;
4. **NOT_RUN** — Real provider live network tests where credentials/binaries are not installed on local host (strictly respecting the Epistemic Rule: no mock substitution).

---

## 1. Real-Provider & Session Ingestion

- **[COMPLETED] Capture provenance & execution metadata**: Timestamps, provider, repository, branch, commit, files, commands, exit codes, tests, edits, and tool evidence.
  - *Evidence:* `model.CodeBinding`, `model.EvidenceRef`, `model.HandoffCheckpoint`, `model.AuthorProvenance`, migration 74 (`claims`), migration 75 (`handoff_checkpoints`). Tested in `internal/epistemic/adversarial_test.go`.
- **[COMPLETED] Durable session checkpoints**: Bounded automatic checkpointing at handoffs and operator checkpoints.
  - *Evidence:* Core #4 `internal/memory/checkpoint/durable_checkpoint.go`, `internal/store/checkpoint.go`, `internal/tui/commands.go` (`/checkpoint`, `/rollback`). Tested in `internal/tui/tui_test.go` and `internal/store/checkpoint_test.go`.
- **[DEFERRED to v1.6.0] OpenCode SQLite Importer**: Retroactive import from external OpenCode SQLite databases.
  - *Rationale:* OpenCode's internal schema varies across minor releases; v1.5 integrates OpenCode as a first-class execution harness via CLI and MATRIX.json rather than coupling to external database files.
- **[NOT_RUN] Authenticated Cross-Provider Live E2E**: End-to-end multi-agent execution across multiple paid external cloud providers (Claude Code, Gemini CLI, Codex, OpenCode).
  - *Rationale:* Epistemic Rule strictly prohibits faking live passes or substituting mocks. Probed via `doctor --probe-providers`. Local hermetic collaborative runtime conformance is verified via `internal/collaboration` and `internal/integration`.

---

## 2. Memory Quality, Consolidation & Governed Conflict

- **[COMPLETED] Governed candidate extraction & failure learning**: Failed approach, reason, environment, evidence, and retry conditions.
  - *Evidence:* `model.MessageFailedApproach`, `model.MessageFinding`, `internal/collaboration/coordinator.go`.
- **[COMPLETED] Failure Fingerprinting & Retry Cut**: Repeated identical error/failure signatures cut blind retries and trigger escalation.
  - *Evidence:* `internal/epistemic/fingerprint.go` (`FingerprintRegistry`). Tested in `internal/epistemic/secondwave_test.go` (`TestFailureFingerprintCutRetry`).
- **[COMPLETED] Temporal Invalidation on Code Change**: Regression oracle invalidates only code-dependent claims when files/commits change, preventing stale-lock cascades.
  - *Evidence:* `internal/epistemic/temporal.go` (`TemporalValidityDiscipline.InvalidateOnCodeChange`). Tested in `internal/epistemic/adversarial_test.go` (`Test9_FineGrainedTemporalInvalidation`).
- **[COMPLETED] Governed Consolidation & Conflict Resolution**: Competing findings remain `CONTESTED` until resolved by deterministic evidence or operator authority.
  - *Evidence:* `internal/epistemic/contradiction.go`, `internal/collaboration/coordinator.go` (`ChallengeClaim`). Tested in `internal/epistemic/adversarial_test.go` (`Test3_DeterministicTestContradictsAgentConsensus`).
- **[COMPLETED] Federation Boundary Specification**: Stable IDs, append-only envelopes, provenance, scope restrictions, tombstones, revocation, and conflicts.
  - *Evidence:* `internal/memory/adaptive/envelope.go`. Remote network federation remains intentionally disabled fail-closed in Community edition.

---

## 3. Multi-Agent Scenarios (T1–T10)

- **[COMPLETED] T1 Codex learns architecture decision**: Published to canonical shared memory with `model.RoleArchitect` provenance.
  - *Evidence:* `internal/collaboration/coordinator.go` (`SendMessage`, `MessageFinding`).
- **[COMPLETED] T2 Governed recall without private leak**: Peer agent receives only relevant governed memory without leaking private transcript.
  - *Evidence:* `internal/collaboration/coordinator.go` (`GetSessionOverview`).
- **[COMPLETED] T3 Evidence-bound failure recording**: Failures recorded with error signature and environment bindings.
  - *Evidence:* `internal/model/collaboration.go` (`MessageFailedApproach`), `internal/epistemic/fingerprint.go`.
- **[COMPLETED] T4 Peer avoids failed approach**: Peer checks failed approach messages from session overview to prevent repeating mistakes.
  - *Evidence:* `internal/collaboration/loop_detector.go` (`LoopRepeatedClaim`, `LoopNoProgress`).
- **[COMPLETED] T5 Code changes make old record stale**: When bound files are modified, formerly verified claims transition to STALE.
  - *Evidence:* `internal/epistemic/temporal.go` (`InvalidateOnCodeChange`).
- **[COMPLETED] T6 Agent proposes supersession**: New claim references `SupersedesID` of predecessor claim.
  - *Evidence:* `model.Claim.SupersedesID`, migration 74 (`claims`).
- **[COMPLETED] T7 Concurrent agents create conflicting findings**: Simultaneous contradictory claims transition state to `CONTESTED`.
  - *Evidence:* `internal/collaboration/coordinator.go` (`ChallengeClaim`).
- **[COMPLETED] T8 Evidence and authority resolve conflict**: Higher-fidelity deterministic tool evidence overrides unverified consensus.
  - *Evidence:* `internal/epistemic/contradiction.go` (`ContradictionDetector.ResolveContradiction`).
- **[COMPLETED] T9 Provider-neutral typed handoff**: Cross-role handoff without transcript replay.
  - *Evidence:* `internal/collaboration/coordinator.go` (`HandOffOwnership`), `internal/protocol/handoff.go`.
- **[COMPLETED] T10 Runtime restart preservation**: All claims, goals, sessions, checkpoints, and budget survive SQLite restarts.
  - *Evidence:* SQLite migrations 73–79, verified in `internal/tui/tui_test.go` and `internal/store/checkpoint_test.go`.

---

## 4. Scale, Performance & Long Soak

- **[COMPLETED] 100k Canonical Record Benchmark**: Verified on Intel Core Ultra 7 255H with raw measurements recorded in `memory-fabric-validation.md`.
- **[DEFERRED to v1.6.0] 250k+ Scale & Long Soak Studies**: Extended multi-day soak test under continuous simulated traffic.
  - *Rationale:* Hardware and test runner wall-clock limits during release gating. Truthful release threshold established at 100k canonical records.
- **[DEFERRED to v1.6.0] Lexical Projection Heap Reduction**: Memory optimization for low-memory embedded hosts (<512MB RAM).
  - *Rationale:* Target host environment meets standard developer workstation profile (16GB+ RAM).
- **[SUPERSEDED] Quality-Series Aggregation for Web**: Replaced by canonical runtime observability in `internal/webcontrol` and `internal/tui/view.go`.

---

## 5. Definition of Release Gates for v1.5.0

All code committed on `feat/v1.5-dev` satisfies:
1. `go test ./...` — PASS across all packages.
2. `go vet ./...` — PASS cleanly.
3. `tools/release_verify.py` — PASS with deterministic `distribution/PACK-MANIFEST.json`.
4. Epistemic integrity — Zero false PASS claims.
