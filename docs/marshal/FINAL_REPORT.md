# Marshal mode final report

Branch: `marshal/m12-docs`
Implementation base: `6f681df6ce64211cda305948354f1c2cafa5b723`
Qualification date: 2026-09-26

## Outcome

The integrated M01–M11 code provides a durable Marshal run, scoped plan approval, headless worker dispatch, runtime-assembled hand-ins, constitutional task acceptance, ordered integration, verification and authorized close. The qualification matrix records 65 PASS, 0 FAIL and 3 NOT_RUN criterion rows. The specified `go test -count=1 -run 'TestM0|TestM1' ./internal/...` passed; targeted tests for criteria whose test names lack those prefixes also passed. These are local test results on the candidate base, not a real-model end-to-end qualification.

## Review history

The task pack's `runs/` history records iterative hand-ins and returns. M01–M03, M06–M08 and M10 received implementation checks; M04's final review found no findings and accepted its hook-safe worktree path. M05 had three review rounds. They identified shared worktree contamination between checks, unbounded output capture, incomplete child cancellation proof, and git hooks and external diff execution during hand-in assembly. The integrated tests cover isolated checkouts, bounded capture, child cancellation, disabled hooks and external diff, runtime reruns, and separation of worker claims from evidence.

M09 review rounds found an integration-check HEAD change that could bind evidence to the wrong commit, races around close and its target worktree, outstanding workers on dispatch or collection failure, and possible custom merge-driver execution. The integrated test suite covers HEAD movement, target-branch selection on detached HEAD, hook suppression, hand-in merge-driver detection, and close checkpoint requirements. The review's direct integration-repository merge-driver execution scenario remains narrower than the hand-in rejection test; the qualification record names this evidence limit.

M11 review found missing real-model PTY evidence, incomplete proof of repaint and Standard-session flow, and gaps in amendment, task approval, concurrency and budget presentation. The documented attempted fix stopped at an app-service boundary: the service saved a proposed amendment immediately, leaving no proposal for the TUI to present for approval or denial. The integrated candidate's M11 unit tests pass, but the three unproved acceptance criteria remain NOT_RUN in the matrix.

## What is not done

- The governed Process 05 worker path is not wired into Marshal mode.
- ULTRA has no independent verifier agent wired into the production service. Runtime check reruns remain mandatory and ULTRA close refuses without an agent.
- No real-model end-to-end run was completed. In particular, M11's PTY start, approval and completed-task criterion is NOT_RUN.
- The M11 review's amendment approval and user task-approval surface concerns are not established as resolved by the named acceptance tests. The repaint and complete Standard-session claims also lack direct proving tests.

The documentation and local tests do not claim that these gaps are closed. No implementation code was changed for M12.

## Integration follow-up

Later changes on `marshal/integration-final` added interactive `/marshal` planning and cached Cloud registration. The wired Marshal model now resolves its provider CLI when no binary override is set, so headless drafting can actually start. A rejected, evidence-backed ULTRA cross-review now returns its task for rework, stores its reasons with the hand-in review and records its verdict with the task decision. Usage charges now have their own `marshal.usage.charged` event. Draft validation compares every scope and check entry with its multiplicity, so duplicate entries cannot conceal a missing approved value. Dispatch checks the driver mode before creating a worktree or starting a worker; a governed task cannot silently launch a native CLI. Process 05 still needs a real governed runner wired into Marshal mode.

The follow-up also added an independent ULTRA model verifier. It uses a provider distinct from the Marshal and every task worker, reviews the integrated worktree, and must return a passing verdict bound to the exact integration commit. Runtime checks still run and a refusal blocks verification and close. Live Codex turns passed `TestMarshalCLIRealModelDraft`, `TestMarshalCLIRealModelReview`, and `TestMarshalCLIRealModelVerifier`; they prove the individual model calls, not a full PTY or worker completion. Major amendments are now proposed without a store mutation, shown for review in `/marshal status`, and applied only after approval with a plan-version check. Denial discards the proposal and restores the original plan view. Native workers without a current governance probe remain blocked before launch, because the completion gate does not permit accepting results from unverified harnesses.

Two qualification gaps remain: Marshal's standalone plan has no canonical Process 03 GoalContract and Process 04 handoff, so the Process 05 governed runner cannot be connected honestly through `ExecutionService.StartRunBound`; governed tasks still fail closed. A real-model PTY test covering start, approval and completed worker work has not run. The existing M11 surface tests and live model draft do not establish that criterion. A pending amendment lives in the active TUI session; after a restart it must be proposed again, while the approved stored plan remains intact.

## Process 05 bridge follow-up

`/marshal use-plan` now binds the current approved, single-task Process 04 plan and confirmed Process 03 goal to a Marshal run. The governed runner calls `ExecutionService.StartRunBound`, executes the task through Process 05 in preserve-branch mode, and imports only the exact completed commit into Marshal's task branch. It checks the stored task, approved files and checks, active plan and goal, assigned harness, base commit, worktree, and resulting commit before import. Process 05 approval pauses the Marshal worker; `/marshal approve-task <approval-id>` decides only an approval bound to the active plan and task, and `/marshal resume` continues. Runner errors do not create hand-ins.

`TestMarshalGovernedTaskUsesProcess05AndImportsExactCommit` passed with a real Process 05 engine and a deterministic mock harness. `TestMarshalBindApprovedPlanRefusesMultipleTasks` proves the current boundary: multi-task Process 04 plans are refused because Process 05 executes its whole plan while Marshal schedules tasks individually. The earlier gap statement describes the state before this bridge. A real provider worker and real-model PTY completion are still unproved.
