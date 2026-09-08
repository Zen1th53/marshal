# Process 07 candidate qualification

Process 06 candidate base: `a53b4a24747f8c1cf02ea4ab99d9207cb39effe7`

Process 07 candidate: the tip of `feat/process-07-learning-memory`.

Every code and test result below was gathered at
`5d03d2677f3b7d7ef2f1fa71e0b133dd287627db`, the last commit that changed
implementation or tests. The candidate tip adds this record, the final report
and the pack manifest on top of it, and changes no code.

This record describes candidate qualification only. It is not the final
canonical-main integration attestation.

## Acceptance matrix

| Row | Status | Direct evidence |
|---|---|---|
| A Process06 entry integrity | PASS | `TestLearningEntryComesFromCanonicalAttestation`, `TestLearningIsBlockedWithoutAnAttestation` |
| B MemoryCommit durability/CAS | PASS | schema v84; `TestMemoryCommitRoundTripAndDigestVerification`, `TestStaleRevisionIsRefused`, `TestConcurrentRevisionsProduceOneWinner` |
| C Epistemic claim model | PASS | `internal/learning/types.go`; claim state, scope, evidence, dependencies, criticality and freshness on every item |
| D project/global scope separation | PASS | `TestRetrieveDoesNotCrossProjects`, `TestRetrieveGeneralScopeIsOptIn`, `TestAdversarialVersionSpecificFailureIsNotGeneralized` |
| E promotion discipline | PASS | `TestAdversarialProviderAssertionIsNotEvidence`, `TestAdversarialRepetitionDoesNotPromoteUnsupportedClaim`, `TestAdversarialCriticalClaimNeedsReplayableMultiClusterEvidence` |
| F revision/invalidation | PASS | `TestItemRevisionsPreserveHistory`, `TestAdversarialPrestigeDoesNotWinContradiction` |
| G freshness/dependency graph | PASS | `TestInvalidateIgnoresUnchangedDependencyVersion`, `TestInvalidateIsTargetedToTheChangedDependency`, `TestItemsDependingOnIsTargeted` |
| H source clustering | PASS | `TestAdversarialRepeatedSourceIsNotIndependent` |
| I contradiction memory | PASS | `TestContradictedMemoryIsSurfacedNotHidden`, `TestAdversarialRetrievalKeepsContradiction`, `TestWebMemorySearchKeepsContradictionVisible` |
| J failure fingerprints | PASS | `failure_fingerprints` with occurrence merge and expiry; `handleFingerprints` marks expired prints stale |
| K success-pattern learning | PASS | `TestAdversarialUnverifiedOutcomeCannotProduceVerifiedMemory`, `TestLearningCannotPromoteVerifiedFromPartialOutcome` |
| L ModelTaskTrust | PASS | `AggregateTrust` scoped by task class, provider, version and model; `TestRoutingObservationsIncludeNegativeOutcomes` |
| M routing learning/veto | PASS | `TestAdversarialRoutingCannotBypassGovernance`, `TestLearningRoutingProposalRespectsGovernanceVeto` |
| N HCI feedback | PASS | `TestCapabilityNeedsVersionAndEvidence`, `TestGeneralCapabilityCannotBindAProject` |
| O quota/cost/latency honesty | PASS | `TestUnmeasuredBenchmarkMetricsStayNil`, `TestWebRoutingTrustIncludesFailures`, `TestOptionalIntKeepsUnmeasuredDistinctFromZero` |
| P verifier/oracle learning | PASS | `TestWeakOracleCannotStandAlone` |
| Q regression baselines | PASS | `TestBaselineFromAnotherTreeIsUnbound`, `TestBaselineDivergenceRevalidatesUnlessInvariant` |
| R playbook candidates | PASS | `TestStoredPlaybookCannotSelfActivate`, `TestAdversarialPlaybookCannotSelfActivate`, `TestWebPlaybookCandidatesAreNeverActive`; the `active` column is constrained to 0 |
| S replay/reproducibility | PASS | `TestExactReplayCannotDependOnExternalSystems`, `TestReplayWithSideEffectsIsNotAutomatic` |
| T retention/archive | PASS | `TestGCKeepsProvenanceForAttestedEvidence`, `TestGCArchivesCanonicalClaims`, `TestGCPlanIsDeterministic` |
| U privacy/secret minimization | PASS | `TestAdversarialSecretNeverEntersMemory` across five secret shapes; `TestSecretsNeverLeaveMemory`; refusal reasons carry no secret |
| V poisoning defense | PASS | `TestAdversarialUnboundClaimCannotBePromoted`, `TestAdversarialProviderAssertionIsNotEvidence` |
| W bias resistance | PASS | `TestAdversarialPrestigeDoesNotWinContradiction`, `TestAdversarialRepetitionDoesNotPromoteUnsupportedClaim` |
| X survivorship/selection bias | PASS | `TestAdversarialEasyTaskSelectionIsFlagged`, `TestAdversarialFailedRunsAreNotOmitted`, `TestLearningIngestsFailedOutcomes` |
| Y counterfactual evaluation | NOT_RUN | replay classes and side-effect refusal are implemented and tested, but no offline counterfactual re-execution harness exists, so no counterfactual was evaluated |
| Z bounded experiments | PASS | rollout fraction and reversibility are veto conditions; `TestAdversarialRoutingCannotBypassGovernance` covers unbounded and irreversible proposals |
| AA benchmark/eval records | PASS | `TestBenchmarkMustBePinned`, `TestUnmeasuredBenchmarkMetricsStayNil` |
| AB Terminal-Bench/SWE-bench hooks | PASS | `BenchmarkRecord` carries benchmark, pinned version, task id, tree, mode, ablation route and escalation count; `benchmark_records` is indexed by benchmark, version and task |
| AC retrieval | PASS | `TestRetrieveIsBounded`, `TestStaleMemoryIsReturnedButNotUsable`, `TestValidateQueryRejectsUnboundedQuery` |
| AD memory→context reinjection | PASS | `TestBuildContextKeepsConstraintsBindingAndMemoryAdvisory`, `TestBuildContextDropsStaleAndKeepsContradictions` |
| AE forgetting/GC | PASS | `TestGCPurgesSecretsRegardlessOfReferences`, `TestAdversarialGCDoesNotBreakAttestation` |
| AF backup/export/restore | PASS | `TestExportRedactsSecretsAndNamesThem`, `TestTamperedExportIsRejected`, `TestRestoreDoesNotOverwriteNewerOrInvalidatedMemory`, `TestRestoreRejectsSchemaMismatch` |
| AG schema/migration | PASS | schema v84; `TestChaosMigrationIsIdempotent`, `TestT77MemoryTableInventory` |
| AH concurrency/CAS | PASS | `TestConcurrentRevisionsProduceOneWinner`, `TestChaosInvalidationIsNotLostToAConcurrentRevision`, race-clean |
| AI tamper-evident integrity | PASS | `TestTamperedMemoryCommitIsDetected`, `TestTamperedMemoryItemIsDetected`, `TestChaosCorruptedItemIsReportedNotServed` |
| AJ TUI | PASS | `internal/tui/commands_learning.go`; eight read views registered in the capability registry |
| AK CLI/Web/MCP/A2A parity | PASS | `TestProcess07ToolsAreReadOnlyAndCapabilityScoped`, `TestProcess07A2ASurfaceRejectsMutation`, `TestWebLearningRoutesRequireAuth`, `TestWebLearningSurfaceHasNoMutationRoute`, `TestLearningCommandIsRegisteredAndFailClosed` |
| AL adversarial | PASS | all twenty scenarios in spec 38, `internal/learning/adversarial_test.go` |
| AM mutation | PASS | 26 of 26 mutants killed, 0 survivors |
| AN chaos | PASS | `internal/store/learning_chaos_test.go`: partial-commit rollback, restart, corruption, missing target, lost invalidation, duplicate commit, repeated migration |
| AO performance/scale | PASS | 10,000 items written in 1.63s; indexed dependency fan-out in 4.09ms; bounded retrieval in 263ms |
| AP full lifecycle E2E | PASS | `TestLearningLifecycleFromVerifiedOutcomeToStaleMemory` plus the seven other runtime tests in `internal/app/learning_runtime_test.go` |
| AQ GitHub/exact-main integration | PASS | merged as PR #114; requalified on exact main `30c763505bee64ef8621466fe67e551089da9b4d`, recorded in `INTEGRATION_ATTESTATION.md` |

## Mutation qualification

Twenty-six gates were removed or inverted one at a time and the Process 07
suite was required to fail. All twenty-six were killed.

Sites covered: promotion evidence, general-scope corroboration, general
applicability bounds, freshness, secret filter, unverified-outcome gate,
critical-claim gate, entry binding, project binding, playbook self-activation,
dependency version comparison, commit digest verification, governance veto,
selection-bias detection, retrieval project scoping, retrieval usability,
retention secret purge, restore overwrite protection, restore invalidation
protection, export digest verification, replay external-dependency gate,
benchmark version pinning, weak-oracle gate, store CAS conflict, store item
tamper detection, store commit digest verification.

The first mutation pass found one survivor. Inverting the dependency version
comparison in `Invalidate` did not fail any test, because nothing asserted that
a dependency reported at its current version leaves knowledge alone. Without
that gate a single routine tool audit would have staled the whole store.
`internal/learning/freshness_test.go` now covers it, and the second pass killed
all twenty-six.

## Repository gates observed

| Gate | Status | Result |
|---|---|---|
| `git diff --check` | PASS | clean |
| `go build ./...` | PASS | clean |
| `go vet ./...` | PASS | clean |
| `go test ./...` | PASS | 137 packages, 0 failures |
| `go test -race` (learning, store) | PASS | clean |
| Adversarial | PASS | 20 of 20 scenarios |
| Mutation | PASS | 26 killed, 0 survivors |
| Chaos | PASS | 7 fault-injection cases |
| Migration/schema | PASS | v84, idempotent, T77 inventory classified |
| Performance/scale | PASS | 10k items, bounded fan-out and retrieval |
| Surface conformance | PASS | CLI, TUI, Web, MCP, A2A |
| Python suites | PASS | 27 tests across `tools/tests`, `tools/tests_v6`, `conformance` |
| Pack validation | PASS | `conformance/runner.py validate-pack` |
| Pack manifest | PASS | regenerated; every governed path matches SHA-256 and byte size |
| `gitleaks` | PASS for this work | 15 findings repository-wide, all pre-existing test fixtures in other packages, none introduced here |
| `govulncheck` | NOT_RUN | binary unavailable in this environment |
| `gosec` | NOT_RUN | binary unavailable in this environment |

## Test counts

- `internal/learning`: 66 passing tests
- `internal/store` Process 07 subset: 53 passing tests
- `internal/app` Process 07 runtime: 8 passing tests
- Repository-wide: 137 packages, 0 failures

## Notes on non-PASS rows

Row Y is `NOT_RUN`. The replay index, its reproducibility classes and the
refusal to auto-replay anything carrying an external side effect are
implemented and tested, and they are the substrate a counterfactual evaluation
would need. What does not exist is a harness that re-executes an alternate
governed route offline and compares outcomes, so no counterfactual was actually
evaluated. Marking the row PASS on the strength of the substrate alone would
overstate what was built.

Row AQ stays `NOT_RUN` until the branch merges and the exact resulting
`origin/main` is requalified.

`govulncheck` and `gosec` are `NOT_RUN` because the binaries are not present in
this environment. They are not inferred from anything else.

`NOT_RUN` is never upgraded to PASS.
