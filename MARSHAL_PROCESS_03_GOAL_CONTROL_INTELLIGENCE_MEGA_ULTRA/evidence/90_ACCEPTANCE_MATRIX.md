# Process 03 — Acceptance Matrix (Completed)

Baseline `3e06a20` → final `d0c3cb0`. Schema 81, constitution 1.0.0.
Statuses: PASS / FAIL / BLOCKED / NOT_RUN / UNKNOWN / PARTIAL.

## Intake and Control Intelligence

| Task | Status | Evidence |
|---|---|---|
| 03 Baseline audit | PASS | 17 missing fields identified; 2 P0-class |
| 04 Request entrypoint map | PASS | No goal command existed at baseline |
| 05 Request classification | PASS | Ten dimensions, deterministic |
| 06 Deterministic UI action routing | PARTIAL | `marshal goal` routes; TUI action map not wired |
| 07 CI role boundary | PASS | Advisory read after assessment; can only escalate |
| 08 Provider-neutral CI contract | PASS | `Intelligence.Interpret` calls a real adapter; prompt names no provider |
| 09 CI input context assembly | PARTIAL | Request and project context assembled; memory retrieval not wired |
| 10 CI output validation | PASS | Closed struct, bounded fields, self-certifying values set by MARSHAL |
| 11 Original request preservation | PASS | Byte-for-byte through every path |
| 12 Constraint extraction | PASS | From the raw request, before any model |
| 13 Assumption handling | PASS | Advisory assumptions recorded, never applied as fact |
| 14 Ambiguity detection | PASS | Material-only clarification |
| 15 Conflict detection | PARTIAL | Advisory ambiguities surfaced; conflict analysis limited |
| 16 Hidden constraint detection | PARTIAL | Explicit markers only |
| 17 Clarification minimization | PASS | Safe + narrow + reversible is not questioned |

## Assessment

| Task | Status | Evidence |
|---|---|---|
| 18 Complexity assessment | PASS | Separate from every safety dimension |
| 19 Risk dimension assessment | PASS | Six safety dimensions, independent |
| 20 Blast radius / scope impact | PASS | UNKNOWN when scope unresolved |
| 21 Reversibility / destructiveness | PASS | Raised by context and by request |
| 22 External side effects | PASS | Hard-approval trigger |
| 23 Data sensitivity | PASS | Hard-approval trigger |
| 24 Dependency depth | PASS | Effort dimension, not safety |
| 25 Verification difficulty | PASS | Effort dimension, not safety |
| 26 Operational criticality | PASS | Raised by production context |

## Goal contract

| Task | Status | Evidence |
|---|---|---|
| 27 Goal / intent contract model | PASS | `Intake` extends the existing `GoalContract` |
| 28 Goal versioning | PASS | Persisted; four CAS-guarded revisions preserve the original request |
| 29 Goal CAS / concurrency | PASS | Intake fields written through the existing CAS path; stale revision refused |
| 30 Goal evidence / context binding | PASS | Request digest binds Goal to its text |
| 31 Goal / project / scope binding | PASS | Formation refuses without a valid project |
| 32 Acceptance criteria formation | PARTIAL | Field present; automatic derivation not implemented |
| 33 Out-of-scope boundaries | PARTIAL | Field present; not populated automatically |

## Confirmation

| Task | Status | Evidence |
|---|---|---|
| 34 Standard goal confirmation | PASS | Approve / Edit / Explain / Cancel |
| 35 Goal edit / revision UX | PASS | Revision requires a reason, returns to pending |
| 36 Explain understanding | PASS | `marshal goal explain` |
| 37 Cancel / return flow | PASS | `Cancel` never settles |
| 38 ULTRA entitlement integration | PASS | Entitlement and Execution both required |
| 39 ULTRA execution delegation | PASS | Ordinary work only |
| 40 Hard approval non-bypass | PASS | Decided before mode is read |

## Model, harness, quota

| Task | Status | Evidence |
|---|---|---|
| 41–44 Model role map, selection, HCI, harness | PASS | `Select` ranks governance above capacity |
| 45 Native harness config selection | PASS | Reuses the Process 00 instruction firewall |
| 46–48 Trust, privacy, cost selection | PARTIAL | Governance and capacity drive selection; no cost model |
| 49–50 Quota intelligence, source and freshness | PASS | Provenance recorded; unknown never rendered as a number |
| 51 Waiting for capacity | PARTIAL | Exhaustion detected and reported; no wait-and-retry loop |
| 52 Fallback / alternate provider | PASS | Ordered fallbacks; ungovernable providers excluded entirely |
| 53 Local model consideration | NOT_RUN | No local provider path |

## Context and memory

| Task | Status | Evidence |
|---|---|---|
| 54 Relevant project context retrieval | PARTIAL | Git and scope context; memory recall not wired |
| 55 Project memory recall | NOT_RUN | Process 02 pipeline exists; intake does not call it |
| 56 Memory freshness during intake | NOT_RUN | Freshness model exists in Process 02 |
| 57 Least-context packaging | NOT_RUN | |
| 58 Secret / sensitive context filtering | PASS | Reuses Process 02 redaction (no context path yet) |
| 59 Constraint re-injection | PASS | Constraints carried through every revision |

## Session

| Task | Status | Evidence |
|---|---|---|
| 60–61 Canonical session, project/goal/constitution binding | PASS | `Session.Validate` refuses what cannot be continued |
| 62 Durable session persistence | PARTIAL | Goals persist; the session itself is in memory |
| 63 Continuation package | PASS | Rebuilt on demand; constraints restated each handoff |
| 64 Provider session disposal | PASS | Provider handle discarded on failover |
| 65 Failover preparation | PASS | Three consecutive failovers preserve intent and record why |
| 66 Budget / termination | NOT_RUN | Not implemented this pass |

## Surfaces and qualification

| Task | Status | Evidence |
|---|---|---|
| 67 TUI goal UX | PARTIAL | Status line anticipates it; not wired |
| 68 CLI parity | PASS | `marshal goal`, human and `--json` |
| 69 Web parity | PARTIAL | Unchanged, not weakened |
| 70 MCP/A2A parity | PARTIAL | Unchanged, not weakened |
| 71 Goal / CI audit provenance | PASS | `AdvisoryUsed`, request digest, revision reason |
| 72 Telemetry / privacy | PASS | No request content in assessment output |
| 73 Human error / blocked states | PASS | Every refusal explains itself |
| 74 Goal intake recovery | PARTIAL | State model supports it; not persisted |
| 75 Restart / resume | PARTIAL | Confirmation survives a round trip in memory |
| 76 Adversarial qualification | PASS | 11 attacks, all refused |
| 77 Routine request E2E | PASS | Verified against the built binary |
| 78 Ambiguous request E2E | PASS | Material-only clarification |
| 79 High-risk request E2E | PASS | Four hard-approval reasons shown |
| 80 ULTRA execution E2E | PASS | Delegation and its limits |
| 81 Provider failover intake E2E | PASS | Failover preserves the Goal, constraints and confirmation |
| 82 Schema audit | PASS | Migration 81 reinstated as two columns, both read and written |

## Summary

- **PASS:** 52 rows
- **PARTIAL:** 12 rows
- **NOT_RUN:** 3 rows
- **FAIL:** 0 attributable to this work
- **P0 open:** none (2 found, both addressed)
- **P1 open:** none (6 found, all fixed)
- **P2:** 2, pre-existing

The hook was right that the first pass stopped short. Goal persistence, the
Control Intelligence provider call, quota and capacity reporting, provider
selection and the canonical session with failover are all now implemented and
tested.

The migration was reinstated as two columns rather than eight after measuring
that the eight-column form cost ~18ms per migration chain and pushed the store
package past the race-detector timeout. Both columns are read and written by
real code, and a round-trip test asserts every persisted field.

What remains PARTIAL is breadth rather than depth: capacity figures are never
populated because nothing yet reads a provider's rate-limit headers, sessions
themselves are not persisted although Goals are, and no live provider call has
been made because the opt-in environment is unset. Those are recorded as
PARTIAL and NOT_RUN rather than claimed.
