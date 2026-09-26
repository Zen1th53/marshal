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
- Budget charges are recorded under the `marshal.task.dispatched` event type instead of distinct decision-stage charge events.
- A rejected ULTRA cross-review stops the run instead of returning the task for rework.
- No real-model end-to-end run was completed. In particular, M11's PTY start, approval and completed-task criterion is NOT_RUN.
- The M11 review's amendment approval and user task-approval surface concerns are not established as resolved by the named acceptance tests. The repaint and complete Standard-session claims also lack direct proving tests.

The documentation and local tests do not claim that these gaps are closed. No implementation code was changed for M12.
