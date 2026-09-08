# Process 04 — Final Report

## Identity

| | |
|---|---|
| Start SHA | `1fdf85a452eeea949c54093443ce8729524ad386` |
| Final SHA | `edb169b2bcc088ffdf193e1efeafe8a25c5d9857` |
| Schema version | 81 → **82** |
| Commits | 10 |
| Branch | `main` |
| Author | `Zen1th53 <extreme29@proton.me>` on every commit; no AI attribution anywhere |

## Commits

| SHA | Subject |
|---|---|
| `43f65a8` | feat(plan): validate goals and resolve a plan-time task graph |
| `fe29989` | feat(plan): assemble teams, plan execution and gate the handoff |
| `5454d2e` | feat(store): persist execution plans with immutable versions |
| `d748e98` | feat(plan): assign harnesses and models by governed priority |
| `a55ba99` | feat(plan): package least-privilege context and predict approval gates |
| `fdd14ac` | feat(plan): compare independent proposals and select best by MARSHAL |
| `1d73b4b` | test(plan): qualify planning against adversarial and end-to-end scenarios |
| `f054a9d` | docs(process04): record implementation state and acceptance matrix |
| `f535cc8` | feat(app): reach planning from the runtime and explain its decisions |
| `edb169b` | chore(release): list Process 04 planning files in the pack manifest |

## Process 00 / 02 / 03 integration

- **Process 00** — every plan records the `constitution.Version` it was formed
  under, read from the Goal rather than asserted by the caller. Governance
  states (`GovernanceVerified` / `Unverified` / `Unavailable`) come from
  `harness.AssessGovernance`, not from a local judgement.
- **Process 02** — plans are bound to a `projectid.ID`. A plan built for one
  project is refused against another at build time and again at the handoff
  gate. Project memory reaches context packages only when fresh and
  project-bound.
- **Process 03** — the Goal is consumed, never recomputed. `Assessment` is
  inherited rather than re-derived, because two disagreeing risk figures would
  be worse than one. `RequiresHardApproval` remains authoritative over any
  per-category rule added here.

## What was built

| Area | Where | Note |
|---|---|---|
| Goal/project validation, staleness | `plan/validation.go` | Six distinct block reasons; hard-constraint digest excludes preferences |
| Task DAG, critical path, parallelism | `plan/dag.go` | Kahn layering; segment-aware path overlap; weight-based critical path |
| Team assembly | `plan/team.go` | Minimum sufficient; every role and every exclusion carries a reason |
| Verification obligations | `plan/team.go` | Records obligations, never results |
| Plan lifecycle, CAS, approval | `plan/execution_plan.go` | Only APPROVED is executable |
| Harness/model/native assignment | `plan/assignment.go` | Seven priority tiers, governance first |
| Least-privilege context packages | `plan/context.go` | Canonical redactor; constraints restated per package |
| Policy and approval prediction | `plan/policy.go` | Nine gate categories; no field can express a grant |
| ULTRA comparison and selection | `plan/ultra.go` | Deterministic; self-scores inert |
| Process 05 handoff gate | `plan/handoff.go` | Re-verifies against live state |
| Durable persistence | `store/plan.go`, migration 82 | Immutable versions, CAS, history |
| Runtime service | `app/plan_runtime.go` | Create / current / approve / cancel / handoff |
| CLI surface | `cli/plan.go` | `plan create \| show \| approve \| cancel \| handoff` |
| Plan explanation | `plan/explain.go` | Pure functions; absent values reported as unrecorded |

## Acceptance matrix

| § | Area | Status | Evidence |
|---|---|---|---|
| A | Input validity | PASS | `TestUnconfirmedGoalBlocksPlanning`, `TestProjectMismatchBlocksPlanning`, `TestGoalRevisionStalesThePlan` |
| B | Canonical plan: persistence, CAS, history, restart | PASS | `TestPlanSurvivesAReopen`, `TestConcurrentWritersCannotOverwriteEachOther`, `TestPlanHistoryIsRetained`, `TestStoredPlanVersionsAreImmutable` |
| C | Task graph | PASS | 9 DAG tests incl. cycle refusal, segment-aware overlap, weighted critical path |
| D | Team | PASS | `TestSmallChangeGetsASmallTeam`, `TestCriteriaCallForAnIndependentChecker` |
| E | HCI routing | PASS | `TestUngovernableHarnessIsNeverAssigned`, `TestNoModelIsInventedWhenTheProbeNamesNone` |
| F | Quota | PASS | `TestKnownZeroQuotaDisqualifiesButUnknownDoesNot`, `TestInventedQuotaIsNotRecorded` |
| G | Context | PASS | `TestEveryPackageRestatesHardConstraints`, `TestSecretsAreFilteredOutOfPackages` |
| H | Security/approvals | PASS | `TestProviderClaimOfPreApprovalChangesNothing`, `TestPlannerCannotAssertPreApproval` |
| I | Checkpoint/rollback | PASS | `TestIrreversibleWorkGetsACheckpointBeforeTheFirstChange` |
| J | Verification | PASS | `TestUncoveredCriterionIsReportedRatherThanInvented`, `TestPlanCannotClaimWorkIsAlreadyVerified` |
| K | Budget | PASS | `TestBudgetBoundsWorkAndPausesWhenWatchingIsWarranted` |
| L | Standard: plan/explain/cancel/approve | PASS | `Summarise`, `ExplainDecision`, CLI `plan` subcommands, `TestPlanCommandIsRegistered` |
| L′ | Standard: **edit** | **NOT_RUN** | No edit path; a plan is revised by rebuilding, not by editing in place |
| M | ULTRA comparison and selection | PASS | 11 tests incl. `TestSelfScoresAreNotCanonicalEvidence` |
| M′ | ULTRA **proposal generation** | **NOT_RUN** | The comparator is complete; no provider is called to produce independent proposals. Proposals are supplied by the caller |
| N | Surface parity: CLI | PASS | `cli/plan.go` reaches `app.PlanService` |
| N′ | Surface parity: **TUI / Web / MCP / A2A** | **NOT_RUN** | None of these surfaces reaches planning |
| O | Process 05 handoff | PASS | `TestFullPathFromGoalToHandoff`, `TestReceiverRejectsAnIncompleteHandoff`, reachable via `marshal plan handoff` |
| — | No worker execution in Process 04 | PASS | `TestPlanningProducesNoExecution`; no execute subcommand exists |

## Tests and gates

| Gate | Status | Detail |
|---|---|---|
| `git diff --check` | PASS | |
| `go vet ./...` | PASS | |
| `go test ./...` | PASS | 131 packages |
| `gofmt` (touched packages) | PASS | |
| `go test -race ./internal/plan/` | PASS | 1.1s |
| `go test -race ./internal/app/` | PASS | 269s |
| `go test -race ./internal/store/` | PASS | 493s against a 600s budget |
| BUILD | PASS | release gate |
| GO TEST RACE | PASS | release gate |
| SANDBOX SECURITY | PASS | release gate |
| POLICY AND AUTHZ | PASS | release gate |
| MEMORY AND MIGRATIONS | PASS | release gate |
| RESOURCE AWARENESS / PROVIDER ADAPTERS / BACKUP AND RECOVERY | PASS | release gate |
| RELEASE TOOLING / PACK CONFORMANCE / LEGACY TOOLING / CLEAN INSTALL | PASS | release gate |
| DOCS AND MANIFEST | PASS | failed mid-work, regenerated at `edb169b`; regression fixed, not excused |
| VULNERABILITY | **FAIL** | Pre-existing: `govulncheck` is built against go1.26 while the toolchain is go1.27. Unrelated to this work |
| WEB CONTROL PLANE | **FAIL** | Pre-existing gate invocation failure. `npm test` (51 files, 118 tests) and `npm run build` both pass when run directly |
| Provider E2E (Codex/Gemini/Claude/OpenCode/parallel) | NOT_RUN | Opt-in via `MARSHAL_TEST_REAL_*`; not set. Standalone provider connectivity is not MARSHAL-mediated E2E and is not claimed as one |

**Process 04 suite: 132 tests, 89.7% statement coverage, race clean.**

## Adversarial results

All twenty required cases are covered. Sixteen are labelled A1–A20 in
`internal/plan/adversarial_test.go`; four are covered elsewhere and are mapped
here so the claim is checkable rather than assumed:

- **A7** (stale memory contradicts the repository) —
  `TestMemoryIsFilteredToFreshProjectBoundAndRelevant` in `context_test.go`
- **A9** (Goal revised mid-plan) — `TestGoalRevisedMidPlanIsCaughtAtBothEnds`
- **A10** (two planners race CAS) —
  `TestConcurrentWritersCannotOverwriteEachOther` in `store/plan_test.go`
- **A16** (budget pressure removes security verification) —
  `TestBudgetPressureCannotRemoveVerification`

A16 was found missing while writing this report. "All twenty" had been written
before the mapping was checked; three of the four unlabelled cases turned out
to be covered elsewhere, and A16 genuinely was not. The test was written rather
than the claim softened.

The cases worth naming:

- **Pre-approval assertion** — injected into every field a planner can
  influence (title, expected output, role). No hard gate is lost. The guarantee
  is structural: no field exists in which a grant can be recorded.
- **Invented quota** — unmeasured capacity stays `Remaining == nil`,
  `ResetsAt == nil`, `Known() == false`; the selection reason carries no figure.
- **Risk hidden by splitting** — five small deletions each draw the same gate
  as one large one, because risk comes from the assessment rather than from
  task granularity.
- **Ungovernable provider with ample capacity** — refused at tier 1, and
  refused again as a fallback.
- **Claim that tests already passed** — unrepresentable; the verification plan
  has no field for a result.

## E2E results

Eighteen scenarios in `internal/plan/e2e_test.go`, all PASS: low-risk fix,
high-risk security change, documentation-only minimal team, unknown quota,
known-zero capacity, ungovernable strongest model, harness disappearance, Goal
revision → stale plan, restart/reload, CAS race, secret filtering, approval
injection, missing-verification blocker, fallback constraint preservation,
ULTRA multi-plan comparison, denied user override, exact Process 05 handoff,
and proof that no worker execution occurs.

## Defects found and fixed during this work

Mutation testing, not coverage, found each of these. Coverage was ≥89%
throughout and would not have surfaced any of them.

1. **Redundant drift check** (`assignment.go`) — a tier-5 version-drift check
   could never fire, because `AssessGovernance` already rejects drift at tier 1.
   Dead code that reads like a safeguard is worse than no check. Removed.
2. **`VerificationCoverage` was a restatement of `GoalCoverage`** (`ultra.go`) —
   both were incremented in the same branch, so one of nine comparison
   dimensions decided nothing. It now counts only criteria a non-mutating task
   serves, since the task making a change is not a check on its own work.
3. **`UnresolvedUnknowns` was the inverse of `GoalCoverage`** (`ultra.go`) —
   exactly `len(criteria) - GoalCoverage`, so ranking on it decided nothing
   coverage had not already decided. Removed as a ranking key, still reported.
4. **Task-tampering test passed for the wrong reason** — appending a task
   tripped the count and routing checks before the digest guard ran, leaving
   the guard untested. It now edits a task in place.
5. **Assessment-fallback test passed through a category rule** — the fixture
   tripped a dimension rule rather than the fallback. It now uses operational
   criticality, the one hard-approval dimension with no category rule of its
   own, which is the only case that isolates the fallback.
6. **Three "not recorded" branches, one covered** (`explain.go`) — a missing
   harness is reported from three separate branches and only the middle one was
   tested, leaving the other two free to invent a value. Each is now pinned.

## P0 / P1 / P2

**P0 — none.** No known defect can cause unsafe execution, data loss, or a
governance bypass. The boundary invariant holds: nothing in Process 04
executes work, and there is no code path from a plan to a running task.

**P1 — blocks the product being complete:**

- **P1.1 ULTRA proposal generation is NOT_RUN.** The comparator is finished and
  qualified, but nothing calls a provider to produce the independent proposals
  it compares. ULTRA today can only rank proposals a caller supplies, which is
  not the feature.
- **P1.2 TUI / Web / MCP / A2A cannot reach planning.** Only CLI does. Spec 20
  requires parity; four of five surfaces have none.
- **P1.3 No plan edit path.** A plan can be revised only by rebuilding it.
  Spec 15 lists edit as a first-class control.

**P2 — worth doing, not blocking:**

- **P2.1** `Restale` is implemented and tested but never called by the runtime;
  nothing detects a Goal revision in the background, so staleness is noticed
  only when the handoff gate refuses.
- **P2.2** Context packages are built but not persisted; they are recomputed on
  demand rather than stored with the plan version they belong to.
- **P2.3** `plan create` requires a JSON task file. Nothing decomposes a Goal
  into tasks automatically, so the caller supplies the decomposition.
- **P2.4** The `VULNERABILITY` gate cannot run locally (toolchain mismatch).
  Pre-existing, but it means this work was never vulnerability-scanned here.

## Limitations, stated plainly

The planning engine is complete, and it is now genuinely reachable: a person
can run `marshal plan create … / show / approve / handoff` and the Process 05
gate will refuse or produce a handoff for real reasons. That is a materially
different claim from the one in `10_IMPLEMENTATION_STATE.md`, which recorded a
library nobody called.

What remains unfinished is not the planning logic but its reach. ULTRA is half
a feature — the half that judges, without the half that generates. Four
surfaces out of five cannot plan at all. Task decomposition is still the
caller's job. None of these is a defect in what was built; each is work that
was not done, and calling them anything else would be the dishonesty this pack
exists to prevent.

Two release gates fail. Both were reproduced as failing before this work and
are recorded as FAIL rather than renamed. The one gate this work did break —
`DOCS AND MANIFEST` — was fixed rather than excused.
