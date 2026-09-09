# Process 08 Qualification

Process 07 base: `ecb7b69e245246eaf0d76eddef4d30f0b03346da`
Exact main at requalification: `f2516f83980e3cfaa766865f172d5cd98a23cbc6`
Pull requests: #116, #120, #121, #122 (merged)

Statuses use only `PASS`, `FAIL`, `BLOCKED`, `NOT_RUN`, and `UNKNOWN`.

This record maps every one of the 51 acceptance rows in the pack's matrix
(`52_ACCEPTANCE_MATRIX.md`) to direct evidence. An earlier revision grouped the
rows into 17 summary lines, which made it impossible to tell which specific
requirement each piece of evidence answered.

## Acceptance matrix

| Row | Status | Direct evidence |
|---|---|---|
| A P07 entry | PASS | `OptimizationService.EntryFor` derives the binding from the stored memory commit; a caller cannot supply the digest, source SHA or outcome |
| B OptimizationCycle durability | PASS | schema v85; `internal/store/optimization_test.go` round-trip, CAS and partial-write rollback |
| C objectives | PASS | `Objective` with explicit direction and weight; constraints outrank objectives by construction in `Promote` |
| D governance veto | PASS | `Veto` reads declared `Effects`, not prose; `TestAdversarialHardGovernanceVetoes` covers 8 weakening classes |
| E candidates | PASS | `ValidateCandidate` requires hypothesis, scope, rollback and verification plan |
| F clustering | PASS | `Cluster`/`ClusterID`; security impact is part of the key, so candidates with different governance effects never merge |
| G baseline | PASS | `Baseline.Valid`/`Comparable`; `Promote` refuses an uncomparable baseline |
| H counterfactual routing | PASS | real execution, not substrate — see the closure section below |
| I replay sandbox | PASS | `ValidateSandbox` refuses production credentials and unbounded resources; `bwrap_replay.go` hardcodes `NetworkAllowed: false` |
| J Terminal-Bench | NOT_RUN | adapter and honest-absence tests exist; no Harbor or `tb` binary on this host, so no benchmark task executed |
| K SWE-bench Verified | NOT_RUN | adapter and official-verdict gate exist; the official evaluator and `swebench` package are absent, so no instance was resolved |
| L single-agent baselines | PASS | `bench/baselines.go`; `TestBaselineSuiteKeepsUnavailableHarnessHonest` — absent harnesses report `NOT_RUN`, not a fabricated score |
| M orchestration ablations | PASS | `RunAblationSuite` executes all six modes; omitted or duplicated outcomes are rejected |
| N reproducibility | PASS | `ValidateManifest` requires pinned benchmark, evaluator, dataset snapshot, tree and environment |
| O metrics | PASS | `Metric.Value` is a pointer; `Measured()` keeps unmeasured distinct from zero |
| P statistics | PASS | `ComparePaired` reports effect size, refuses an interval below 20 judged pairs, and flags weak evidence |
| Q flaky quarantine | PASS | `DetectFlaky`/`QuarantineFlaky` quarantine every attempt of a nondeterministic task |
| R shadow | PASS | `ValidateShadow` refuses secret access and external effects; `TestAdversarialShadowAndExplorationFailClosed` |
| S canary | PASS | `ValidateCanary` requires scope, task bound, deadline and triggers; refuses full-traffic exposure |
| T promotion | PASS | `Promote` answers governance first; a veto is terminal regardless of score |
| U rollback | PASS | `EvaluateGuard`/`Rollback`; an unmeasured trigger metric rolls back rather than passing |
| V no self-modifying governance | PASS | `Enact` always refuses; `Classify` routes material change back through Process 03 |
| W policy boundary | PASS | `ValidateProposal` requires current policy, limitation, evidence, security impact, rollback and verification plan |
| X routing | PASS | `SupportsRouting` requires multi-cluster, net-positive, non-synthetic evidence |
| Y cascade/escalation | PASS | `DimCascade` candidates pass the same veto and promotion gates |
| Z verifier selection | PASS | `DimVerifier`; `RemovesMandatoryVerification` is a hard veto |
| AA context | PASS | `DimContext` dimension under the same governance path |
| AB native harness | PASS | `Route.NativeEffort` and `HarnessVersion` are part of route identity and baseline pinning |
| AC resource | PASS | `SandboxPolicy` bounds wall time and memory; replay is refused without them |
| AD cost/budget | PASS | `SpendsVerificationReserve` is a hard veto; `Baseline.BudgetMicros` is nil when unset |
| AE latency | PASS | latency is a `Metric` with provenance; unmeasured stays nil |
| AF quota adaptation | PASS | `CauseRateLimit` quarantine; unmeasured reset stays UNKNOWN |
| AG drift | PASS | `DetectDrift` across model, harness, benchmark, tool, policy and environment; `StaleAfterDrift` |
| AH explore/exploit | PASS | `AllowExploration` refuses high-risk work, secret exposure and destructive effects |
| AI product tier | PASS | `EnablesFleetControl` is vetoed outside the Enterprise tier |
| AJ P07 feedback | PASS | `ValidateFeedback` requires results behind a promotion; `SelfReinforcing` blocks evidence loops |
| AK playbooks | PASS | `EvaluatePlaybook` requires applicability, adversarial verification, real evidence and a canary |
| AL poisoning | PASS | cluster counting, pinned baselines and `TestAdversarialEvidenceCannotBeCherryPickedOrSelfReinforced` |
| AM holdout | PASS | a holdout regression rejects promotion regardless of development gains |
| AN eval integrity | PASS | exclusions require reasons; unofficial runs cannot be labelled official |
| AO TUI | PASS | `internal/tui/commands_optimization.go`; five capabilities registered in the capability registry |
| AP surfaces | PASS | 12 surface tests across CLI, Web, MCP, A2A and TUI; every Process 08 surface is read-only |
| AQ restart | PASS | `Recover`/`ApplyRecovery`/`InterruptedNeverPasses`; `OptimizationService.Recover`; 12 tests |
| AR concurrency | PASS | CAS on cycle version returning `ErrConflict`; store concurrency tests |
| AS result integrity | PASS | digest verification on cycles, counterfactuals, manifests and promotion records |
| AT adversarial | PASS | `internal/optimization/adversarial_test.go`, table-driven across the pack's scenarios |
| AU mutation | PASS | `internal/optimization/mutation_test.go` covering critical gates and replay safety |
| AV chaos | PASS | `internal/optimization/chaos_test.go` |
| AW performance | PASS | 1,000 candidates and 10,000 results aggregated in 21.9ms |
| AX full lifecycle E2E | PASS | `TestProcess03Through08Lifecycle` through real Process 03 to 08 services |
| AY exact-main integration | PASS | requalified on `f2516f83980e3cfaa766865f172d5cd98a23cbc6`; see `INTEGRATION_ATTESTATION.md` |

Counts: 49 PASS, 2 NOT_RUN, 0 FAIL, 0 BLOCKED, 0 UNKNOWN.

## Primary closure: counterfactual routing

Process 07 left this row `NOT_RUN`, and the pack states that substrate alone is
not a PASS. The closure is real execution:

`bwrap_replay.go` runs the alternate route as a child process through
`exec.CommandContext` under Bubblewrap, with `NetworkAllowed: false` hardcoded
and path containment enforced.
`TestBwrapReplayRunnerExecutesWithRealBubblewrapWhenAvailable` executed against
the Bubblewrap binary on this host. `ExecuteReplay` refuses before invoking the
runner when the work is unsafe or the sandbox would enable network or
production credentials. A counterfactual against a factual run that performed a
destructive external effect is refused outright, and synthetic-only evidence
cannot justify a routing change.

## Exact-main gates

`git diff --check` PASS; `go build ./...` PASS; `go vet ./...` PASS;
`go test ./...` PASS; selected `go test -race` PASS across optimization, bench,
app and store; pack and result integrity PASS; 27 Python suite tests PASS.

## Rows that are not PASS

Rows J and K are `NOT_RUN` because neither official harness is installed on this
host. Docker is running and the network is reachable, so the blocker is the
absent harness rather than the environment. The adapters exist and their tests
prove they report `NOT_RUN` rather than fabricating a score, which is precisely
why the rows are not inferred from the adapters that would consume them.

No `NOT_RUN`, `BLOCKED`, or `UNKNOWN` row is authorization for promotion, and
none is upgraded on the strength of adjacent evidence.
