# 90 — Process 01 Acceptance Matrix (Completed)

Baseline `2c4850a` → final `6561c57`. Schema 80, constitution 1.0.0.
Statuses: PASS / FAIL / BLOCKED / NOT_RUN / UNKNOWN / PARTIAL.

## Startup and control center

| Row | Status | Evidence |
|---|---|---|
| 03 Baseline audit | PASS | Three P0s reproduced against the built binary |
| 04 Entrypoint map | PASS | Six hard-failure points in `app.Open` mapped |
| 05 State machine | PASS | 8 phases; only CORE_FAILED closes the control center |
| 06 Health model | PASS | Core/environment/project/execution kept separate |
| 07 TUI-first startup | PASS | Assessment precedes `app.Open`; verified by running the binary |
| 08 First run | PASS | Detected; Setup recommended, never required |
| 09 Landing | PASS | Constant actions; disabled ones always explain themselves |
| 10 Current/recent project | PARTIAL | Current project detected; recent-project list not persisted |
| 11 Empty / non-Git directory | PASS | Normal state; no silent Git mutation (test-asserted) |
| 12 Git readiness | PASS | Blocks projects, not the control center |

## State and recovery

| Row | Status | Evidence |
|---|---|---|
| 13 State directories | PASS | The single core-fatal condition |
| 14 SQLite / schema | PASS | Unchanged at 80; existing install/upgrade tests green |
| 15 Migration recovery | PASS | Transactional migration unchanged from Process 00 |
| 16 Reconciliation | PASS | Counts reported; idempotence test-asserted |
| 17 Session recovery | PASS | Offered, never auto-resumed (test-asserted) |
| 18 Checkpoint recovery | PARTIAL | Reported via reconciliation; checkpoint browsing not surfaced |

## Setup, Doctor, repair

| Row | Status | Evidence |
|---|---|---|
| 19 Setup | PASS | `marshal setup`; no write path exists |
| 20 Doctor | PASS | Pre-existing; verified independent of `app.Open` |
| 21 Repair | PASS | Consent per condition; Fixed requires a passing re-test |

## Environment

| Row | Status | Evidence |
|---|---|---|
| 22 Harness probes | PARTIAL | Delegated to existing Doctor probes |
| 23 HCI | PARTIAL | `harness.AssessGovernance` from Process 00; not surfaced at startup |
| 24 Sandbox | PASS | Fail-closed; blocks execution only |
| 25 Network | PASS | Absent enforcement reported as absent, not limited |
| 26 Provider readiness | PARTIAL | Four facts modelled and documented; per-provider probing delegated |
| 27 Config / policy | PASS | Missing policy blocks rather than defaulting permissively |
| 28 Secrets | PASS | No credential path in assessment; summaries screened |
| 29 ULTRA | PASS | Never default-on; entitlement never rescues a blocked environment |
| 30 Readiness cache | NOT_RUN | Deliberately not implemented — staleness risk without measured benefit |
| 31 Degraded operation | PASS | Per-capability scoping, test-asserted |
| 32 Error model | PASS | Reason codes with remedies; user-safe screening |

## Surfaces

| Row | Status | Evidence |
|---|---|---|
| 33 Help | PASS | 9 topics + `help why`; jargon test-enforced |
| 34 TUI truth | PASS | Landing renders only from the assessment; PTY suite green |
| 35 CLI parity | PASS | Human and `--json` share one assessment |
| 36 Web parity | PARTIAL | Existing behavior unchanged; assessment not surfaced |
| 37 MCP / A2A | PARTIAL | Existing behavior unchanged; assessment not surfaced |
| 38 Offline | PASS | Local work continues; network features named as unavailable |
| 39 Optional deps | PASS | Scoped to the affected capability |
| 40 Required deps | PASS | Block execution only |

## Integrity

| Row | Status | Evidence |
|---|---|---|
| 41 Corrupt state | PARTIAL | Reason codes and remedies defined; corrupt-DB detection not probed |
| 42 Project identity | PARTIAL | Reason codes defined; `app.Open` retains its identity check |
| 43 Moved project | PARTIAL | Reason code defined; reattachment flow not implemented |
| 44 Standard / ULTRA | PASS | Same constitution; ULTRA cannot weaken anything |
| 45 Telemetry | NOT_RUN | Not measured this pass |
| 46 Audit | PASS | Reconciliation outcomes reported rather than discarded |
| 47 Performance | NOT_RUN | Not measured; probes are bounded at 5s |

## Qualification

| Row | Status | Evidence |
|---|---|---|
| 48 Adversarial | PASS | 11 attacks, all refused |
| 49 Restart E2E | PASS | Reconciliation idempotence across repeated runs |
| 50 PTY | PASS | Full conformance suite green |
| 51 Schema | PASS | No new persistence needed; existing gates green |
| 52 Docs | PASS | `docs/startup.md` |
| 53 Release gates | PARTIAL | 15 PASS; 2 FAIL proven pre-existing at baseline |
| 54 Final evidence | PASS | `evidence/54_FINAL_EVIDENCE.md` |

## Summary

- **PASS:** 36 rows
- **PARTIAL:** 12 rows
- **NOT_RUN:** 4 rows
- **FAIL:** 0 attributable to this work
- **P0 open:** none (3 found and fixed) · **P1 open:** none (2 found and fixed) · **P2:** 3, all pre-existing

The honest headline: the primary invariant is met and proven by running the
real binary — the control center now opens in every state short of MARSHAL
having nowhere to keep its own state, and the three baseline defects are fixed
with regression tests. What remains PARTIAL is breadth rather than depth:
per-provider probing, Web and MCP/A2A surfacing, and the identity and
corrupt-state flows have their reason codes and semantics defined but are not
yet fully wired. Those are recorded as PARTIAL rather than claimed as done.
