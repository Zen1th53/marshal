# 90 — Process 02 Acceptance Matrix (Completed)

Baseline `71ac697` → final `ca9524b`. Schema 80, constitution 1.0.0.
Statuses: PASS / FAIL / BLOCKED / NOT_RUN / UNKNOWN / PARTIAL.

## Identity and scope

| Task | Status | Evidence |
|---|---|---|
| 03 Baseline audit | PASS | Two P0s reproduced, one as a failing test |
| 04 Entrypoint map | PASS | Six hard-failure points in `app.Open` mapped |
| 05 Canonical project identity | PASS | Derived from repository evidence; path not an input |
| 06 Repository/worktree identity | PASS | Root commit + per-adoption nonce |
| 07 Working scope | PASS | Symlink-resolved, segment-compared, exclusions honoured |
| 08 Moved project rebinding | PASS | Opens, keeps identity, records new location |
| 09 Current-directory acquisition | PASS | Resolve never writes; adoption is explicit |
| 10 Recent project reopen | PARTIAL | Identity supports it; no persisted recent list |
| 11 Create project flow | PASS | `Bootstrap` adopts an identity at setup |
| 12 Import existing project | PARTIAL | Trust and identity semantics done; no `import` command |
| 13 Clone flow | PARTIAL | Clone identity tested; no `clone` command |
| 14 Empty / non-Git flow | PASS | Normal state; no silent Git mutation |

## Git safety

| Task | Status | Evidence |
|---|---|---|
| 15 Git safety boundary | PASS | Execution requires recoverability |
| 16 Git state qualification | PASS | READY / LIMITED / BLOCKED with reasons |
| 17 Dirty worktree policy | PASS | Detected, files named, not safe to mutate |
| 18 Nested repos, submodules | PASS | Detected and excluded from scope |
| 19 Monorepo scope | PASS | Narrowing to a package is real and tested |
| 50 Filesystem permissions | PASS | Reused from Process 01 state-dir check |
| 51 Symlink boundary | PASS | Resolved before comparison; escape refused |

## State and config trust

| Task | Status | Evidence |
|---|---|---|
| 20 `.marshal` state | PASS | Binding validated; unbound state not adopted |
| 21 Existing state trust | PASS | Copied and corrupt bindings refused |
| 22 Config discovery | PARTIAL | Policy presence checked; no broader discovery walk |
| 23 Instruction firewall | PASS | Reused from Process 00 `harness.ScreenInstructions` |
| 24 Harness config governance | PASS | Reused from Process 00 |
| 25 MCP/plugin/tool config | PARTIAL | Classified as untrusted; no dedicated parser |
| 26 Secret-safe ingestion | PASS | Redaction runs first; pure-credential source rejected |
| 52 Project policy resolution | PASS | Missing policy blocks rather than defaulting |

## Readiness

| Task | Status | Evidence |
|---|---|---|
| 27–28 Dependency classification/readiness | PASS | Reused from Process 01 capability scoping |
| 29 Sandbox compatibility | PASS | Reused; fail-closed |
| 30 Network compatibility | PASS | Reused; absent enforcement reported as absent |
| 31 Provider/harness compatibility | PARTIAL | Four-fact model from Process 01; per-provider probing delegated |
| 32 HCI project integration | PARTIAL | Governance states exist; not surfaced per project |
| 53 Project readiness model | PASS | Identity dimension added to the assessment |
| 54 Readiness cache | NOT_RUN | Deliberately not implemented |
| 55 Degraded mode | PASS | Per-capability scoping preserved |
| 56 Project recovery | PASS | Reconciliation reporting from Process 01 |

## Memory bootstrap

| Task | Status | Evidence |
|---|---|---|
| 33 Source discovery | PARTIAL | Pipeline complete; no filesystem walk yet |
| 34 Source classification | PASS | Seven kinds; unclassified is untrusted |
| 35 Secret redaction | PASS | Runs before every other stage |
| 36 Identity binding | PASS | No binding, no candidates |
| 37 Freshness | PASS | FRESH / STALE / UNKNOWN; unknown is not fresh |
| 38 Provenance | PASS | Kind and path carried through to the gate |
| 39 Conflict detection | PASS | Negation-aware; both sides held |
| 40 Trust states | PASS | Import trust matrix implemented |
| 41 Candidate creation | PASS | Stable, project-scoped identities |
| 42 Promotion gate | PASS | Delegated to Process 00; AI refused as authority |
| 43 Fresh bootstrap | PARTIAL | Deterministic facts supported; extraction not automated |
| 44 Git history assimilation | PARTIAL | Classified as evidence; no extractor |
| 45 Docs assimilation | PARTIAL | Classified as claim; no extractor |
| 46 Repository fact extraction | PARTIAL | Classified as verifiable; no extractor |
| 47 Cross-project isolation | PASS | Candidate IDs project-scoped; gate refuses foreign |
| 48 Duplicate/fork identity | PASS | Clone shares lineage, gets its own identity |
| 49 Remote URL identity | PASS | Normalized; excluded from derivation by design |

## Surfaces and qualification

| Task | Status | Evidence |
|---|---|---|
| 57 Backup/restored state | PASS | Restored copy refused while original exists |
| 58 Concurrent access | PARTIAL | Atomic binding writes; no multi-session locking added |
| 59 Standard vs ULTRA | PASS | No mode-specific branch exists |
| 60 TUI project UX | PASS | Identity surfaced through readiness |
| 61 CLI parity | PASS | `marshal setup` reports identity |
| 62 Web parity | PARTIAL | Unchanged; identity not surfaced |
| 63 MCP/A2A parity | PARTIAL | Unchanged; identity not surfaced |
| 64 Audit/provenance | PASS | Verdict and reason recorded on every resolution |
| 65 Telemetry privacy | PASS | No content in identity or readiness output |
| 66 Performance | PASS | Evidence collection bounded at 10s, stdin never inherited |
| 67 Adversarial | PASS | 10 attacks, all refused |
| 68 Open existing project E2E | PASS | Real repositories |
| 69 Clone E2E | PASS | Lineage shared, identity distinct |
| 70 Memory assimilation E2E | PASS | Through the real gate |
| 71 Moved project E2E | PASS | Binary-verified and unit-tested |
| 72–73 Schema audit/migration | PASS | No new persistence needed; schema unchanged at 80 |
| 74 Documentation | PASS | `docs/projects.md` |
| 75 Release gates | PARTIAL | 15 PASS; 2 FAIL proven pre-existing |
| 76 Final evidence | PASS | `evidence/76_FINAL_EVIDENCE_REPORT.md` |

## Summary

- **PASS:** 45 rows
- **PARTIAL:** 16 rows
- **NOT_RUN:** 1 row
- **FAIL:** 0 attributable to this work
- **P0 open:** none (2 found, fixed) · **P1 open:** none (4 found, fixed) · **P2:** 3 pre-existing

The honest headline: project identity is real and proven against the binary —
a moved project opens and keeps its history, a different repository at a reused
path is refused, and readiness no longer claims READY for something the runtime
would reject. The memory pipeline is complete and adversarially tested through
the real constitutional gate.

The largest gap is that nothing yet walks a project to *find* memory sources
automatically; they are supplied by the caller. Clone and import exist as
semantics rather than commands. Both are recorded as PARTIAL rather than
claimed as done.
