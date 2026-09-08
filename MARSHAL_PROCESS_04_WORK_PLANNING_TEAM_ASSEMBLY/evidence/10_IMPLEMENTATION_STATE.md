# Process 04 — Implementation State

Recorded against the working tree at the seventh Process 04 commit.
Statuses use only PASS / FAIL / BLOCKED / NOT_RUN / UNKNOWN.

## The single most important fact

`internal/plan` is imported by **no surface and no runtime**. Every call site
of `Build`, `SavePlan`, `AssignHarnesses`, `BuildContextPackages`, `SelectBest`
and `PrepareHandoff` is inside the package itself or its tests.

Verified by:

```
grep -rn "SavePlan\|GetActivePlan\|PrepareHandoff\|AssignHarnesses\|\
BuildContextPackages\|SelectBest" --include='*.go' . \
  | grep -v "^./internal/plan/" | grep -v "^./internal/store/plan"
# → only declarations and in-package tests
```

```
grep -rln "internal/plan" --include='*.go' \
  internal/cli/ internal/tui/ internal/mcp/ internal/a2a/ \
  internal/app/ internal/httpsrv/
# → no matches
```

The planning engine is complete and tested as a library. Nothing a user can
reach invokes it. Every section below must be read against that: the logic is
qualified, the product path is not.

## Acceptance matrix

| § | Area | Status | Evidence / limitation |
|---|---|---|---|
| A | Input validity — current Goal, stale/cancelled/mismatched rejected | PASS | `validation.go`; `TestUnconfirmedGoalBlocksPlanning`, `TestProjectMismatchBlocksPlanning`, `TestGoalRevisionStalesThePlan` |
| B | Canonical plan — persistence, CAS, revision history, restart | PASS | schema 82; `TestPlanSurvivesAReopen`, `TestConcurrentWritersCannotOverwriteEachOther`, `TestPlanHistoryIsRetained`, `TestStoredPlanVersionsAreImmutable` |
| C | Task graph — coverage, DAG validity, critical path, parallel safety | PASS | `dag.go`; 9 tests incl. `TestCycleIsRefusedAndNamesTheTasks`, `TestPathOverlapUsesSegmentsNotPrefixes`, `TestCriticalPathFollowsWeight` |
| D | Team — minimum sufficient, fixed roles, independence | PASS | `team.go`; `TestSmallChangeGetsASmallTeam`, `TestCriteriaCallForAnIndependentChecker`, `TestSecuritySensitiveWorkAddsAppSec` |
| E | HCI routing — governance first, version-aware, no invented model/settings, safe fallback | PASS | `assignment.go`; `TestUngovernableHarnessIsNeverAssigned`, `TestNoModelIsInventedWhenTheProbeNamesNone`, `TestUnsupportedSettingsAreOmittedRatherThanInvented`, `TestFallbackNamesWhatMustBeRevalidated` |
| F | Quota — UNKNOWN/zero distinction, provenance, no invented reset | PASS | `TestKnownZeroQuotaDisqualifiesButUnknownDoesNot`, `TestInventedQuotaIsNotRecorded` |
| G | Context — least privilege, fresh/project-bound, secret-safe, constraints re-injected | PASS | `context.go`; `TestEveryPackageRestatesHardConstraints`, `TestMemoryIsFilteredToFreshProjectBoundAndRelevant`, `TestSecretsAreFilteredOutOfPackages`, `TestCapabilitiesAreLeastPrivilege` |
| H | Security/approvals — hard approval preserved, capability/sandbox/network planning | PASS | `policy.go`; `TestProviderClaimOfPreApprovalChangesNothing`, `TestPlannerCannotAssertPreApproval`, `TestPolicyStatesAllowedAndDeniedScope` |
| I | Checkpoint/rollback — meaningful boundaries, irreversible effects honest | PASS | `TestIrreversibleWorkGetsACheckpointBeforeTheFirstChange` |
| J | Verification — acceptance coverage, critical coverage, independence, no theatre | PASS | `TestUncoveredCriterionIsReportedRatherThanInvented`, `TestHardToUndoWorkMarksCriteriaCritical`, `TestPlanCannotClaimWorkIsAlreadyVerified` |
| K | Budget — honest known/unknown, verification preserved | PASS | `TestBudgetBoundsWorkAndPausesWhenWatchingIsWarranted` |
| L | Standard mode — plan/explain/edit/cancel/execute | **NOT_RUN** | Only `Cancel` exists (`execution_plan.go:401`) and `BlockReason.Explain`. Inspect, edit, explain-decision and execute are unimplemented, and nothing exposes them |
| M | ULTRA — independent proposals, canonical comparison, safe override | **PARTIAL** | Comparison and Best-by-MARSHAL selection are implemented and tested (`ultra.go`, 10 tests). Proposals are supplied by the caller: **no provider is called** to generate independent proposals, and there is no per-role override path |
| N | Surface parity — TUI/CLI/Web/MCP/A2A | **NOT_RUN** | No surface imports `internal/plan` |
| O | Process 05 handoff — exact Goal + Plan, required refs, no worker execution | PASS (library) | `handoff.go`; `TestFullPathFromGoalToHandoff`, `TestReceiverRejectsAnIncompleteHandoff`, `TestPlanningProducesNoExecution`. No runtime path reaches it |

## Repo gates

| Gate | Status | Note |
|---|---|---|
| `git diff --check` | PASS | no whitespace errors |
| `go vet ./...` | PASS | clean |
| `go test ./...` | PASS | 131 packages, 0 failures |
| `gofmt` (changed packages) | PASS | clean |
| Process 04 unit/E2E/adversarial | PASS | 118 tests, 92.5% coverage |
| `go test -race ./internal/plan/` | PASS | 1.1s |
| `go test -race ./internal/store/` | PASS | 493s against a 600s budget |
| Migration / schema | PASS | `TestT77MemoryTableInventory`, migration tests |
| Web tests | PASS | 51 files, 118 tests |
| Web build | PASS | built in 299ms |
| Release gate (`scripts/community-release-gate.sh`) | UNKNOWN | BUILD, GO VET, GO TEST reported PASS; the run was interrupted during GO TEST RACE, so the full result is not established |
| `govulncheck` | BLOCKED | not installed locally; pre-existing toolchain condition, unrelated to this work |
| TUI parity, CLI/Web/MCP/A2A conformance | NOT_RUN | nothing to conform: no surface integration exists |

## Migration cost

Schema 82 adds two tables in one `CREATE TABLE` exec. Measured against a
baseline worktree with `BenchmarkMigrationChain`:

- without migration 82: ~45ms per chain
- with migration 82: ~46ms per chain

Within run-to-run noise. The store package races in 493s (600s budget). This
was measured rather than assumed because schema 81's eight per-column `ALTER`s
cost ~18ms per chain, replayed by ~150 store tests, which previously pushed
the package past its timeout.

## What remains

1. **Runtime integration** — nothing constructs, stores or hands off a plan
   outside tests. This is the largest remaining piece and the reason L, N and
   the conformance gates are NOT_RUN.
2. **Plan controls** (spec 15) — inspect, edit, explain, execute.
3. **ULTRA proposal generation** (spec 16) — the comparator is done; the
   provider calls that would produce independent proposals are not.
4. **Surface parity** (specs 19, 20).
5. **Final report** (spec 29).

## Honest reading

Sections A–K and O qualify the planning logic, and that logic is genuinely
tested: 118 tests, and every security-relevant invariant was checked by
mutation testing rather than by coverage alone. Several mutation runs found
real defects — a redundant drift check that could never fire, two ULTRA
comparison dimensions that decided nothing, and three tests that passed for
the wrong reason.

But a library nobody calls cannot be said to work as a product. The correct
summary of Process 04 at this commit is: **the planning engine is built and
qualified; the product path through it does not exist yet.**
