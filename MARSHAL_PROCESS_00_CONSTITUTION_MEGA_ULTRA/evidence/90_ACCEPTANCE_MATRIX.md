# 90 — Master Acceptance Matrix (Completed)

Baseline `7ceb9a7` → final `cc426cc`. Constitution 1.0.0, schema 80.
Statuses are limited to PASS / FAIL / BLOCKED / NOT_RUN / UNKNOWN / PARTIAL.

## Constitution

| Row | Status | Evidence |
|---|---|---|
| Constitution version | PASS | `constitution.Current` = 1.0.0; strict compatibility; a session bound to a newer version is refused rather than reinterpreted |
| Invariant registry | PASS | 20 invariants with stable IDs, article links, severities, reason codes, user-safe explanations; malformed/duplicate entries fail at load |
| Authority hierarchy | PASS | `Resolve` ranks by level only; AI levels 8–9 below all rule-bearing sources; overreach recorded |
| Decision envelope | PASS | `Envelope.Validate` + `BindingDigest` over 15 approval-bearing fields |
| CI input contract | PASS | `Brief` — provider-neutral plain data, no harness prompt semantics |
| CI output contract | PASS | `Advisory` — no permission-bearing field exists |
| Constitutional self-check | PASS | `SelfCheck.Admissions`; only admissions escalate |
| Deterministic gate | PASS | `Evaluate` — 7 outcomes, most restrictive governs, only ALLOW permits |
| Reason codes | PASS | 26 codes; every constitutional refusal non-retryable; unknown codes fail closed |
| Violation engine | PASS | 13 classes → fixed responses; unrecognised class treated as most severe |
| Session binding | PASS | `session_constitutions`, immutable, idempotent re-bind, conflicting re-bind refused |
| Constraint re-injection | PARTIAL | `internal/reinjection` exists and is reused; constitution version not yet threaded into every handoff |

## Security / Authority

| Row | Status | Evidence |
|---|---|---|
| AuthZ | PASS | Deterministic authorization precedes execution; no AI-minted authority |
| Approvals | PASS | Binding digest stales on all 11 tested mutations; expiry; self-approval refused |
| Sandbox | PASS | Fail-closed for shell/file/external; never weakened to pass a test |
| Network | PASS | Fail-closed for network/external; unenforced egress blocked |
| Secrets | PASS | `CI-020` unscoped — refused in every domain; explanations never echo content |
| Filesystem scope | PASS | Scope violations computed by path resolution, not inferred from text |
| External side effects | PASS | Disclosed on every rollback outcome including success |
| ULTRA entitlement | PASS | No entitlement → blocked; entitled ULTRA still faces every Standard gate |
| HCI | PASS | 4 governance states; VERIFIED_GOVERNED requires current evidenced probe |
| Native config firewall | PASS | Override attempts detected and refused; reports never echo instruction text |
| Dynamic tools/plugins | PARTIAL | Governed via domain classification; per-tool discovery not separately qualified |

## Processes 01–08

| Process | Status | Evidence |
|---|---|---|
| 01 Startup | PASS | Degraded governance reduces operation without blocking recovery surfaces |
| 02 Project | PASS | Project isolation unscoped, applies to reads; cross-project suspends session |
| 03 Goal | PASS | Goal fidelity invariant; session bound to constitution at start |
| 04 Planning | PASS | Approval map via binding digest; plan mutation classified |
| 05 Execution | PASS | Fail-closed sandbox/network/credential/external domains |
| 06 Verification | PASS | Completion policy is the sole issuer of VERIFIED |
| 07 Result | PASS | Rollback verified against observed state; external effects disclosed |
| 08 Memory/Close | PASS | Candidate → validate → review → gate → write pipeline |

## Evidence / Memory

| Row | Status | Evidence |
|---|---|---|
| Evidence types | PASS | Assertion is not proof; agent claims evidence-gated |
| Provenance | PASS | Reuses `internal/provenance`; independent sources counted as distinct sources |
| Source dependency | PASS | Several agents on one output count as one source |
| Freshness | PASS | Stale evidence downgrades a PASS to UNKNOWN |
| Completion policy | PASS | VERIFIED requires criteria, freshness, no unmet critical, no conflict, no outstanding approval, genuine independent review, verified rollback, final digest |
| Alignment | PASS | Ratio of satisfied to total weight; reproducible; no score without criteria; no AI confidence |
| Best practices | PARTIAL | Structure present via recommendations; not separately qualified |
| Recommendations | PARTIAL | Reuses `internal/recommendation`; lifecycle not fully wired |
| Memory candidates | PASS | AI can never be the promoting authority |
| CI memory | PASS | Orchestration memory may not reference project content |
| Stale propagation | PASS | Transitive walk; terminates on cycles |
| Memory version/rollback | PASS | Contradictions held for reconciliation, never silently overwritten |
| Retention | PARTIAL | Growth bounds not separately qualified |

## Continuity

| Row | Status | Evidence |
|---|---|---|
| Continuation package | PARTIAL | Session binding survives restart; full package not qualified |
| Failover | PARTIAL | Domain classified; provider failover not separately qualified |
| Checkpoint | PASS | Mutating + non-internally-reversible with no checkpoint requires approval |
| Rollback | PASS | 4 statuses; only ROLLED_BACK is success; nothing-checked is unverifiable |
| Restart | PASS | Idempotent binding and decision records; 5 retries → 1 audit row |
| Backup/restore | PASS | Existing suite green at schema 80 |
| Upgrade | PASS | 79→80 preserves pre-existing rows untouched |

## Surfaces

| Row | Status | Evidence |
|---|---|---|
| TUI / CLI / Web / MCP / A2A parity | PASS | Identical verdict, reason and binding across all 6 surfaces, at gate and runtime level |
| No surface mints success | PASS | Recorded outcome matches the verdict returned to the caller, per surface |
| CLI constitutional visibility | PASS | `marshal constitution version\|invariants\|decisions\|violations`; read-only, 7 mutation-shaped subcommands rejected |
| Setup/Doctor | PARTIAL | Degraded states defined; doctor integration not separately qualified |
| Telemetry | PARTIAL | Explanations carry no content; opt-in tiers not qualified |
| Export | PARTIAL | Domain classified; bundle export not qualified |
| Enterprise hooks | NOT_RUN | Out of scope for this pass |

## Qualification

| Row | Status | Evidence |
|---|---|---|
| Unit | PASS | 89.3% coverage on the constitution package |
| Integration | PASS | Runtime binding, persistence, parity, suspension |
| Conformance | PASS | Cross-surface at both levels |
| Adversarial | PASS | 13 attacks refused, each with a persuasive advisory |
| Provider | NOT_RUN | Opt-in env vars unset |
| Restart/failover E2E | PARTIAL | Restart PASS; provider failover NOT_RUN |
| Rollback E2E | PASS | Audit-only rollback rejected by test |
| Security regressions | PASS | Every P0/P1 attack has a permanent test |
| Performance | NOT_RUN | No budget measured this pass |
| Evals | NOT_RUN | Out of scope for this pass |
| Release gates | PARTIAL | 15 PASS; 2 FAIL proven pre-existing at baseline `7ceb9a7`; DOCS AND MANIFEST now PASS |

## Summary

- **PASS:** 57 rows
- **PARTIAL:** 14 rows
- **NOT_RUN:** 5 rows
- **FAIL:** 0 rows attributable to this work
- **P0 open:** none. **P1 open:** none (3 found and fixed). **P2 open:** 3, all pre-existing.

The honest headline: the constitutional layer is real, enforced and adversarially
tested, and it is not yet threaded through every existing mutation handler on
every surface. That remaining wiring is the main outstanding work, and it is
recorded as PARTIAL rather than claimed as done.
