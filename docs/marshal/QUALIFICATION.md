# Marshal mode qualification

Candidate: `marshal/m12-docs`, implementation base `6f681df6ce64211cda305948354f1c2cafa5b723`. On 2026-09-26, `go test -count=1 -run 'TestM0|TestM1' ./internal/...` passed. A second targeted run covered the M01, M02, M06 and M08 tests whose names lack those prefixes, plus `TestT77MemoryTableInventory`; `TestModelInventoryNeverInvokesClaude` passed separately. PASS below means the named proving test ran and passed in these runs. NOT_RUN means the stated behavior lacks a proving run; a nearby passing test is not promoted into evidence for it.

## Acceptance matrix

| Criterion | Status | Proving test or missing evidence |
| --- | --- | --- |
| M01.1 Illegal state transitions rejected | PASS | `TestStateTransitions` |
| M01.2 Two returns, reassignment, escalation | PASS | `TestReworkPolicy` |
| M01.3 Per-task scoped versus major amendment | PASS | `TestAmendmentClassification` |
| M01.4 Self-review, including Marshal execution | PASS | `TestSelfReview` |
| M01.5 Out-of-scope hand-in rejected | PASS | `TestHandInScopeAndEvidence` |
| M01.6 Settings defaults and enums | PASS | `TestSettingsDefaultsAndEnums` |
| M01.7 Unknown budget and task/plan ceilings | PASS | `TestBudgetUnknownAndCeilings` |
| M01.8 Changed or removed checks are major | PASS | `TestChecksChangeIsMajor` |
| M01.9 Close authorization digest binding | PASS | `TestCloseAuthorizationDigest` |
| M02.1 Fresh and current-head migration | PASS | `TestMarshalMigrationFreshAndCurrentHead` |
| M02.2 Stale write changes nothing | PASS | `TestMarshalStaleRevisionLeavesStateUnchanged` |
| M02.3 Run, tasks, hand-ins and reviews round-trip | PASS | `TestMarshalRunTaskHandInReviewRoundTrip` |
| M02.4 T77 inventory unchanged | PASS | `TestT77MemoryTableInventory` |
| M02.5 Settings default and invalid enum | PASS | `TestMarshalSettingsDefaultsAndInvalidEnum` |
| M03.1 Scoped split remains approved and handoff works | PASS | `TestM03ScopedSplitRetainsApprovalAndHandoff` |
| M03.2 Scope, budget and goal expansion rejected | PASS | `TestM03ScopeExpansionAndRemovalRejected` |
| M03.3 Cross-task redistribution rejected | PASS | `TestM03RedistributionAcrossTasksRejected` |
| M03.4 Split requires route and assignment | PASS | `TestM03SplitRequiresRouteAndAssignment` |
| M03.5 Ordinary revision withdraws approval | PASS | `TestM03RevisionStillWithdrawsApproval` |
| M03.6 Stale concurrent amendment fails | PASS | `TestM03ConcurrentAmendmentRejectsStaleVersion` |
| M04.1 Default Process 05 delivery and lease | PASS | `TestM04DefaultDeliveryExistingProcess05`, `TestM04DefaultDeliveryReleasesLease` |
| M04.2 Preserve commit, untouched root and worktree | PASS | `TestM04PreserveBranchCommitRootBaseResultAndLease` |
| M04.3 No directory-copy fallback | PASS | `TestM04PreserveBranchNeverCopiesOnGitFailure` |
| M04.4 Base and result commits recorded | PASS | `TestM04PreserveBranchCommitRootBaseResultAndLease` |
| M04.5 Lease released in both modes | PASS | `TestM04DefaultDeliveryReleasesLease`, `TestM04PreserveBranchCommitRootBaseResultAndLease` |
| M04.6 Rework resumes existing branch | PASS | `TestM04ReworkResumesExistingBranchAtResult` |
| M04.7 Restart reattaches worktree | PASS | `TestM04RestartReattachesBranchAtRecordedResult` |
| M05.1 Headless drivers with closed stdin | PASS | `TestM05DriversRunWithoutTTY` |
| M05.2 Diff and files obtained from git | PASS | `TestM05HandInFromGitNotWorkerText` |
| M05.3 Runtime rerun records failing check | PASS | `TestM05ChecksRerunDespiteClaimedSuccess` |
| M05.4 Worker reports kept apart from evidence | PASS | `TestM05WorkerReportedKeptApart` |
| M05.5 Cancel stops worker and preserves worktree | PASS | `TestM05CancelStopsWorkerKeepsWorktree` |
| M05.6 No Claude discovery in driver | PASS | `TestM05NoClaudeDiscoveryPath` |
| M06.1 Recommendation never invokes Claude | PASS | `TestModelInventoryNeverInvokesClaude` |
| M06.2 Deterministic ranking | PASS | `TestRecommendDeterministicRanking` |
| M06.3 Empty inventory names missing sources | PASS | `TestRecommendEmptyInventoryNamesMissingSources` |
| M06.4 Rank and route first choice agree | PASS | `TestRankAdvancedFirstEqualsRouteAdvanced` |
| M07.1 Failing criterion denies acceptance | PASS | `TestM07FailingCriterionDeniesMarshalAccept` |
| M07.2 Executor cannot review own task | PASS | `TestM07ReviewerExecutorInvariant` |
| M07.3 Marshal executor cannot self-review | PASS | `TestM07MarshalExecutorCannotReview` |
| M07.4 ULTRA requires cross-review | PASS | `TestM07UltraRequiresCrossReview` |
| M07.5 Evidence bound to result commit | PASS | `TestM07EvidenceBoundToResultCommit` |
| M07.6 User mode requires person | PASS | `TestM07UserModeRequiresPerson` |
| M07.7 Verdict never becomes approval actor | PASS | `TestM07MarshalVerdictNeverSetsApprovalActor` |
| M07.8 Standing close authorization enforced | PASS | `TestM07CloseRequiresCurrentUserAuthorization` |
| M08.1 No lease means Standard | PASS | `TestTierPolicyNoLeaseUsesStandard` |
| M08.2 ULTRA lease uses settings | PASS | `TestTierPolicyMarshalLeaseUsesSettings` |
| M08.3 Expiry affects next dispatch only | PASS | `TestTierPolicyExpiryAffectsNextDispatchOnly` |
| M08.4 Cross-review uses another provider | PASS | `TestCrossReviewRequiresDifferentProvider` |
| M09.1 Two-task plan through close | PASS | `TestM09HappyPathTwoTasksVerifyClose` |
| M09.2 Return twice, reassign, escalate | PASS | `TestM09ReturnTwiceReassignThenEscalate` |
| M09.3 Major amendment pauses dispatch | PASS | `TestM09MajorAmendmentPausesDispatch` |
| M09.4 Merge conflict returns at integration head | PASS | `TestM09MergeConflictReturnsAtIntegrationHead` |
| M09.5 Close on detached HEAD | PASS | `TestM09CloseUsesProjectBranchOnDetachedHEAD` |
| M09.6 Resume from store | PASS | `TestM09ResumeFromStore` |
| M09.7 Plan ceiling: tokens | PASS | `TestM09PlanBudgetTokensPausesRun` |
| M09.7 Plan ceiling: wall time | PASS | `TestM09PlanBudgetWallTimePausesRun` |
| M09.7 Plan ceiling: money | PASS | `TestM09PlanBudgetMoneyPausesRun` |
| M09.8 Task ceiling returns task, run continues | PASS | `TestM09TaskBudgetReturnsAndRunContinues` |
| M09.9 Native unknown usage is not zero | PASS | `TestM09NativeUnknownUsageNotZero` |
| M10.1 Event vocabulary validates | PASS | `TestM10MarshalEventVocabulary` |
| M10.2 Event lifecycle validates | PASS | `TestM10MarshalEventLifecycle` |
| M10.3 Seal chain detects tampering | PASS | `TestM10AcceptedSealChainTampering` |
| M10.4 Reopened decisions remain ordered | PASS | `TestM10MarshalDecisionsReopenSequence` |
| M11.1 Command returns before planning finishes | PASS | `TestM11MarshalReturnsBeforePlanningFinishes` |
| M11.2 State change appears within one repaint | NOT_RUN | `TestM11PanelShowsTasks` renders a constructed snapshot; no state-change-to-repaint test |
| M11.3 Full Standard flow with gated navigation | NOT_RUN | `TestM11MarshalReturnsBeforePlanningFinishes` begins in Standard but its fake model fails before approval and task completion |
| M11.4 PTY start, approve, task completion | NOT_RUN | No named PTY test with a real Marshal model was run |

## Security properties added in review

| Property | Status | Proving test |
| --- | --- | --- |
| Runtime git commands cannot invoke repository hooks | PASS | `TestM04WorktreeOperationsDoNotRunHooks`, `TestM04PreserveBranchCommitDisablesRepositoryHooks`, `TestM05GitHooksDisabledDuringAssembly`, `TestM09MergeDoesNotRunRepositoryHooks` |
| Runtime git evidence excludes external diff and textconv | PASS | `TestM05ExternalDiffAndTextconvDisabled` |
| Hand-ins reject configured custom merge drivers | PASS | `TestM09DetectsMergeDriverInHandIn` |
| Acceptance evidence comes from runtime reruns | PASS | `TestM05ChecksRerunDespiteClaimedSuccess`, `TestM05WorkerReportedKeptApart` |
| Checks use isolated result checkouts | PASS | `TestM05ChecksAreIsolatedFromEachOther` |
| Runtime never invents a commit identity | PASS | `TestM05MissingIdentityFailsInsteadOfInventing`, `TestM04PreserveBranchCommitUsesConfiguredIdentityAndTaskMessage` |
| Close requires a real checkpoint and fresh integrated evidence | PASS | `TestM07CloseRequiresCheckpointOrIrreversibilityStatement`, `TestM07CloseRequiresFreshIntegratedEvidence`, `TestM09WiredVerificationFailsWhenCheckMovesHead` |

The custom merge-driver test proves rejection at hand-in validation. It is not a direct test of a driver configured on the integration repository during the merge itself.
