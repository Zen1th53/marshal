# Process 08 Qualification

Branch: `feat/process-08-governed-optimization`
Process 07 base: `ecb7b69e245246eaf0d76eddef4d30f0b03346da`
Pull request: #116

Statuses use only `PASS`, `FAIL`, `BLOCKED`, `NOT_RUN`, and `UNKNOWN`.

| Requirement | Status | Direct evidence |
|---|---|---|
| P07 exact binding / durable OptimizationCycle | PASS | `internal/app/optimization_runtime.go`, `internal/store/optimization_test.go` |
| Objectives, veto, candidate clustering, baseline | PASS | `internal/optimization/{engine,governance}.go`, adversarial and mutation tests |
| Safe counterfactual routing replay | PASS | `internal/optimization/bwrap_replay.go`; `TestBwrapReplayRunnerExecutesWithRealBubblewrapWhenAvailable` |
| Replay sandbox isolation | PASS | `internal/sandbox/bwrap_test.go`; real Bubblewrap test passed on this host |
| Terminal-Bench integration | NOT_RUN | adapter and test coverage exist; Harbor binary was absent from PATH |
| Official SWE-bench Verified | NOT_RUN | adapter and official-verdict gate coverage exist; official evaluator was absent from PATH |
| Single-agent baselines | PASS | `internal/optimization/bench/bench_test.go`; unavailable harnesses report `NOT_RUN` |
| Six orchestration ablations | PASS | `TestAblationSuiteRequiresAllSixModes` |
| Reproducibility / metrics / statistics / flaky quarantine | PASS | `internal/optimization/experiment.go`, `internal/optimization/bench/*` |
| Shadow, canary, promotion, rollback, drift | PASS | `internal/optimization/rollout.go`, lifecycle and adversarial tests |
| No self-modifying governance / tier boundary | PASS | `internal/optimization/governance.go`, adversarial tests |
| CLI and Web read surfaces | PASS | `internal/cli/optimization.go`, `internal/webcontrol/optimization.go` |
| TUI / MCP / A2A parity | NOT_RUN | no Process 08 handlers registered yet |
| Adversarial and mutation qualification | PASS | `internal/optimization/{adversarial,mutation}_test.go` |
| Chaos / performance scale | NOT_RUN | focused CAS race test passed; full fault and scale matrix has not run |
| P06→P07→P08 lifecycle | PASS | `TestOptimizationLifecycleFromVerifiedLearningThroughReplayAndRollback` |
| Exact-main integration requalification | BLOCKED | PR #116 CI and merge are pending |

Current local gates: `go vet ./...` PASS; `go test ./...` PASS; `git diff --check` PASS.

No `NOT_RUN`, `BLOCKED`, or `UNKNOWN` row is authorization for promotion.
