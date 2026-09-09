# Process 08 pack coverage

Every numbered specification in
`MARSHAL_PROCESS_08_GOVERNED_OPTIMIZATION_CONTINUOUS_EVOLUTION_FINAL_MEGA_ULTRA/`
mapped, in order, to the symbol or test that satisfies it on exact main
`7d10a702a70ef704acd0eb06825f8a3a0d2b472d`.

This exists because a summary of coverage is not coverage. Grouping
requirements into themes made two missing implementations invisible until the
rows were walked one at a time.

| Spec | Requirement | Satisfied by |
|---|---|---|
| 01 | P07 entry contract | `OptimizationService.EntryFor`, derived from the stored memory commit |
| 02 | OptimizationCycle model | `Cycle`, `AppendOptimizationCycle`, `UpdateOptimizationCycle` (CAS) |
| 03 | Objective function | `Objective` with explicit direction and weight |
| 04 | Hard constraint veto | `Veto` reading declared `Effects` |
| 05 | Candidate generation | `Candidate` requiring rollback and verification plans |
| 06 | Candidate clustering | `Cluster`, `ClusterKey` keyed on security impact |
| 07 | Pinned baseline | `Baseline`, `Comparable` |
| 08 | Counterfactual routing | `Counterfactual`, `Compare`, `AggregateRouteEvidence` |
| 09 | Replay sandbox | `ValidateSandbox`, `NetworkAllowed: false` |
| 10 | Terminal-Bench | `bench/terminal.go`, `BenchmarkTerminal` |
| 11 | SWE-bench Verified | `bench/swebench.go`, `OfficialResolution` |
| 12 | Single-agent baselines | `bench/baselines.go` |
| 13 | Six ablations | `RequiredAblations`, `RunAblationSuite` |
| 14 | Reproducibility | `BenchmarkManifest`, `ValidateManifest` |
| 15 | Metric normalization | `Metric.Value` pointer, `Measured()` |
| 16 | Statistical discipline | `ComparePaired`, `WeakEvidence`, `EffectSize` |
| 17 | Flaky quarantine | `DetectFlaky`, `QuarantineFlaky`, `QuarantineCause` |
| 18 | Shadow mode | `ShadowPolicy`, `ValidateShadow` |
| 19 | Canary rollout | `Canary`, `ValidateCanary`, `RequiresFullReview` |
| 20 | Promotion decision | `Promote` with all six decision outcomes |
| 21 | Rollback guard | `EvaluateGuard`, `Rollback`, `Trigger` |
| 22 | No self-modifying governance | `Classify`, `ChangeMaterial`, `RequiresLifecycleReturn` |
| 23 | Policy change proposals | `PolicyProposal`, `ValidateProposal`, `Enact` (always refuses) |
| 24 | Routing optimization | `DimRouting`, `SupportsRouting` |
| 25 | Cascade / escalation | `DimCascade` |
| 26 | Verifier selection | `DimVerifier`, `RemovesMandatoryVerification` veto |
| 27 | Context strategy | `DimContext` |
| 28 | Native harness settings | `Route.NativeEffort`, `HarnessVersion` |
| 29 | Resource aware | `SandboxPolicy.MaxWallMillis`, `MaxMemoryBytes` |
| 30 | Cost / budget | `SpendsVerificationReserve` veto, nil-able `BudgetMicros` |
| 31 | Latency / throughput | latency as a provenance-carrying `Metric` |
| 32 | Rate limit adaptation | `CauseRateLimit` quarantine |
| 33 | Drift detection | `DetectDrift`, `StaleAfterDrift`, `DriftKind` |
| 34 | Explore / exploit | `AllowExploration`, `ExplorationPolicy` |
| 35 | Product tier boundary | `EnablesFleetControl` unconditionally vetoed in Community |
| 36 | P07 feedback | `Feedback`, `ValidateFeedback`, `SelfReinforcing` |
| 37 | Playbook promotion | `PlaybookPromotion`, `EvaluatePlaybook` |
| 38 | Poisoning defense | cluster counting, pinned baselines |
| 39 | Holdout / overfitting | `HoldoutResults` rejection in `Promote` |
| 40 | Eval integrity | `Exclusion` reasons, official-run gating |
| 41 | TUI optimization UX | `commands_optimization.go` |
| 42 | Surface parity | five registry capabilities across CLI, Web and TUI |
| 43 | Durability / restart | `Recover`, `ApplyRecovery`, `InterruptedNeverPasses` |
| 44 | Concurrency / leases | CAS returning `ErrConflict` |
| 45 | Tamper-evident results | `Verify` on cycles, counterfactuals, manifests, promotions |
| 46 | Adversarial tests | `adversarial_test.go` |
| 47 | Mutation / fault injection | `mutation_test.go` |
| 48 | Chaos / resilience | `chaos_test.go` |
| 49 | Performance / scale | `TestProcess08ScaleCandidateAndResultAggregation` |
| 50 | Full P03→P08 E2E | `TestProcess03Through08Lifecycle` |
| 51 | Process09 handoff | N/A. The pack says not to invent Process 09; Process 08 terminates cleanly after its durable decision and Process 07 feedback |
| 52, 90 | Acceptance matrix | all 51 rows mapped in `QUALIFICATION.md` |
| 53 | Repository gates | every named gate run on exact main; results in `QUALIFICATION.md` |
| 54 | Exact-main requalification | all 13 steps; results in `INTEGRATION_ATTESTATION.md` |
| 55 | P07 carry-forward | verified against actual main rather than trusted |
| 56 | Task graph | P08-01 through P08-33 covered by the rows above |
| 57 | Definition of done | all 24 clauses verified against real symbols |
| 58 | Final report template | `FINAL_REPORT.md`, in the template's own field order |

Specs 01 through 50 all resolve to real implementation. Spec 51 is correctly
not applicable. Specs 52 through 58 are process requirements, satisfied by the
records named above.

## Requalification SHA note

The three records above were written at `7d10a702a70ef704acd0eb06825f8a3a0d2b472d`
and merged as `5070439d9e7f01b66f4a141074df44cdbf8fd241`. The two trees differ
only by these documents and their manifest entries: `git diff --name-only`
between them reports no change outside `docs/` and `distribution/`. Every gate
result recorded here was re-run on the merged tree and still passes.
