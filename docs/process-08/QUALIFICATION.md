# Process 08 Qualification

Process 07 base: `ecb7b69e245246eaf0d76eddef4d30f0b03346da`
Exact main: `a1de3f70144a51670bab02f1384e28f9d8dcb151`
Pull requests: #116, #120, #121 (all merged)

Every row below was re-run against the exact main tree named above, not carried
forward from a candidate branch.

Statuses use only `PASS`, `FAIL`, `BLOCKED`, `NOT_RUN`, and `UNKNOWN`.

| Requirement | Status | Direct evidence |
|---|---|---|
| P07 exact binding / durable OptimizationCycle | PASS | `internal/app/optimization_runtime.go`, `internal/store/optimization_test.go` |
| Objectives, veto, candidate clustering, baseline | PASS | `internal/optimization/{engine,governance}.go`, adversarial and mutation tests |
| Safe counterfactual routing replay | PASS | `internal/optimization/bwrap_replay.go`; `TestBwrapReplayRunnerExecutesWithRealBubblewrapWhenAvailable` |
| Replay sandbox isolation | PASS | `internal/sandbox/bwrap_test.go`; real Bubblewrap test passed on this host |
| Terminal-Bench integration | NOT_RUN | adapter and test coverage exist and behave honestly when the harness is absent; no Harbor or `tb` binary is installed on this host, so no benchmark task was executed |
| Official SWE-bench Verified | NOT_RUN | adapter and official-verdict gate coverage exist; the official evaluator and `swebench` package are not installed, so no instance was resolved. Only an official-harness verdict would count |
| Single-agent baselines | PASS | `internal/optimization/bench/bench_test.go`; unavailable harnesses report `NOT_RUN` |
| Six orchestration ablations | PASS | `TestAblationSuiteRequiresAllSixModes` |
| Reproducibility / metrics / statistics / flaky quarantine | PASS | `internal/optimization/experiment.go`, `internal/optimization/bench/*` |
| Shadow, canary, promotion, rollback, drift | PASS | `internal/optimization/rollout.go`, lifecycle and adversarial tests |
| No self-modifying governance / tier boundary | PASS | `internal/optimization/governance.go`, adversarial tests |
| CLI and Web read surfaces | PASS | `internal/cli/optimization.go`, `internal/webcontrol/optimization.go` |
| TUI / MCP / A2A parity | PASS | `TestProcess08ToolsAreReadOnlyAndCapabilityScoped`, `TestProcess08Wire*`, `TestProcess08A2ASurfacesAreAuthenticatedAndReadOnly`, `TestOptimizationCommandIsReadOnlyAndFailClosed` |
| Adversarial and mutation qualification | PASS | `internal/optimization/{adversarial,mutation}_test.go` |
| Chaos / performance scale | PASS | `internal/optimization/chaos_test.go`; `TestProcess08ScaleCandidateAndResultAggregation` at 1,000 candidates and 10,000 results in 21.9ms |
| Full P03→P08 lifecycle E2E | PASS | `TestProcess03Through08Lifecycle` drives real `goalintake.Form`/`Approve`, `runtime.Plans()`, `Execution()`, `Verification()`, `Learning()` and `Optimization()`, then a bounded canary rollback that preserves its reason |
| Exact-main integration requalification | PASS | requalified on `a1de3f70144a51670bab02f1384e28f9d8dcb151`; see `INTEGRATION_ATTESTATION.md` |

Exact-main gates: `git diff --check` PASS; `go build ./...` PASS; `go vet ./...` PASS;
`go test ./...` PASS; selected `go test -race` PASS across optimization, bench, app
and store; pack and result integrity PASS; 27 Python suite tests PASS.

No `NOT_RUN`, `BLOCKED`, or `UNKNOWN` row is authorization for promotion.
