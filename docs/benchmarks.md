# Benchmarks and evaluation

This page describes the evaluation architecture on current `main` and, separately,
what has actually been executed. The two are not the same thing, and conflating
them is the failure this document exists to prevent.

An adapter unit test is not a benchmark result. A passing test proves the
integration behaves correctly, including when the benchmark harness is absent. It
proves nothing about a score.

## Status vocabulary

| Status | Meaning |
|---|---|
| `IMPLEMENTED` | The integration exists in source and is covered by tests |
| `TESTED` | Its own unit tests pass |
| `OFFICIAL BENCHMARK EXECUTED` | The benchmark's own harness ran and produced a verdict |
| `NOT_RUN` | Not executed. Never inferred from adjacent evidence |

## Current state

| Evaluation | Implemented | Tested | Official run | Notes |
|---|:---:|:---:|:---:|---|
| Terminal-Bench | yes | yes | **NOT_RUN** | No Harbor or `tb` binary is installed on the qualification host, so no benchmark task was executed |
| SWE-bench Verified | yes | yes | **NOT_RUN** | The official evaluator and `swebench` package are absent, so no instance was resolved |
| Single-agent baselines | yes | yes | n/a | Comparison plumbing; unavailable harnesses report `NOT_RUN` rather than a score |
| Orchestration ablations | yes | yes | n/a | All six modes execute deterministically |
| Counterfactual routing | yes | yes | n/a | Executes real sandboxed replay; see below |
| Replay sandbox | yes | yes | n/a | Bubblewrap, network off, production credentials refused |

**No MARSHAL benchmark score is published anywhere in this repository**, because
none has been produced by an official evaluator. If you find a number presented
as a Terminal-Bench or SWE-bench result, it is a bug in the documentation.

## Why the rows above stay NOT_RUN

Docker runs on the qualification host and the network is reachable. The blocker
is that neither official harness is installed — not the environment. The adapters
are written so that this situation produces an honest `NOT_RUN` per pinned task
rather than a fabricated result, and their tests assert exactly that:

- `TestTerminalBenchUnavailableIsNotRunForEveryPinnedTask`
- `TestSWEBenchDoesNotTurnMissingOfficialEvaluatorIntoFailure`

For SWE-bench Verified, only the official evaluator's verdict counts as a
resolution. The type system enforces the distinction: `NewOfficialResolution` and
`NewUnofficialResolution` are separate constructors, and an unofficial run cannot
be relabelled official.

## Orchestration ablations

Six configurations, defined in `internal/optimization/experiment.go`:

| Mode | What runs |
|---|---|
| `SINGLE_MODEL` | One model, no orchestration |
| `ROUTING_ONLY` | MARSHAL routing, nothing else |
| `ROUTING_CASCADE` | Routing plus escalation |
| `ROUTING_CRITIQUE` | Routing plus a critic |
| `ROUTING_VERIFY` | Routing plus independent verification |
| `FULL_ULTRA` | Full orchestration |

`RunAblationSuite` requires all six against the same pinned task set. A suite
missing a mode is rejected as incomplete, and an outcome that is omitted or
duplicated for any mode fails the run rather than being quietly averaged away.

## Counterfactual routing

Counterfactual evaluation asks whether another governed route would have reached
a better verified outcome on a task that already ran. It is the one evaluation
here that executes real work.

The alternate route runs as a child process under Bubblewrap with the network
disabled and production credentials refused. Safety is checked before the runner
is invoked, not after:

- A counterfactual against a run that performed a **destructive external effect**
  is refused outright. Re-running it would repeat the effect.
- A run marked non-replayable is honoured rather than second-guessed.
- Verified outcome dominates the comparison. A route that was cheaper and faster
  but never reached a verified outcome did not do better.
- Evidence built entirely from synthetic fixtures cannot justify a routing change.

## Reproducibility

Every evaluation run carries a manifest pinning benchmark version, evaluator
version, dataset snapshot, task set, MARSHAL SHA, config digest, model and
harness versions, environment image and seeds. `ValidateManifest` refuses a
manifest that cannot authorize a comparison, because a score without provenance
cannot be reproduced and so cannot justify a change.

Task exclusions must carry a reason. An undisclosed exclusion is how a hard
subset quietly disappears from a score.

## Metrics

Unmeasured values stay nil and render as `unmeasured`. A cost nobody measured is
not a cost of zero, and reporting it as one lets a candidate win on a number that
was never observed.

Statistical claims are bounded by sample size: no confidence interval is reported
below twenty judged pairs, and comparisons below that threshold are flagged as
weak evidence rather than dressed in false precision.

## Related

- Agent and doctrine evaluation: [`EVALS.md`](../EVALS.md)
- Process 08 qualification record: [`process-08/QUALIFICATION.md`](process-08/QUALIFICATION.md)
