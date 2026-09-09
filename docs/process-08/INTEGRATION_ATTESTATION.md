# Process 08 integration attestation

Status: `PROCESS 08 VERIFIED AND INTEGRATED`

Exact main SHA: `a1de3f70144a51670bab02f1384e28f9d8dcb151`
Exact main tree: `033e716e3eda9bf335c3b78576422ef993bcc111`
Process 07 base: `ecb7b69e245246eaf0d76eddef4d30f0b03346da`
Merges: PR #116, PR #120, PR #121 (#121 merged 2026-09-09T04:54:08Z)

This attestation is bound to the exact main named above. It does not carry to
any other tree.

## Ancestry and tree identity

Every qualified candidate is an ancestor of this main:

| Candidate | Ancestor of main |
|---|---|
| `80a5710eb025688acb161f13b6fc205925e1d3d9` | yes |
| `099feeddf99090e351b310ece409803fb6e5779b` | yes |
| `f2874dc` (lifecycle E2E) | yes |
| `836f1d4dcca8f856d8bb8658ffe787b780fdecb9` (final candidate) | yes |

The final candidate's tree is `033e716e3eda9bf335c3b78576422ef993bcc111`, which
is byte-identical to the exact main tree. The requalified tree is therefore the
tree that was reviewed, not a merge artifact of it.

## Primary closure: the Process 07 counterfactual gap

Process 07 closed with counterfactual routing evaluation at `NOT_RUN`. It had
built the replay index but had never evaluated a counterfactual with it, and
substrate alone was explicitly not a PASS.

That gap is closed by real execution, not by the substrate:

- `internal/optimization/bwrap_replay.go` runs the alternate route through
  `exec.CommandContext` under Bubblewrap. It is a real child process in a real
  sandbox, not a simulated outcome.
- `TestBwrapReplayRunnerExecutesWithRealBubblewrapWhenAvailable` executed
  against the Bubblewrap binary present on this host (`/sbin/bwrap`).
- `ExecuteReplay` refuses before invoking the runner when the work is unsafe or
  the sandbox would enable network or production credentials, proven by
  `TestExecuteReplayRefusesUnsafeWorkBeforeCallingRunner` and
  `TestExecuteReplayRefusesNetworkEnabledSandboxBeforeCallingRunner`.
- A counterfactual is refused outright where the factual run performed a
  destructive external effect, because re-running it would repeat the action.
- Evidence assembled entirely from synthetic fixtures cannot justify a routing
  change; replay, benchmark or shadow evidence is required.

23 counterfactual and replay tests pass on exact main.

## Gates rerun on exact main

| Gate | Status | Result |
|---|---|---|
| `git diff --check` | PASS | clean |
| `go build ./...` | PASS | clean |
| `go vet ./...` | PASS | clean |
| `go test ./...` | PASS | 0 failures |
| `go test -race` (optimization, bench, app, store) | PASS | clean |
| Counterfactual routing and replay sandbox | PASS | 23 tests, real Bubblewrap execution |
| Six orchestration ablations | PASS | all six modes required and executed; omitted or duplicated outcomes rejected |
| Single-agent baseline plumbing | PASS | unavailable harnesses report `NOT_RUN` rather than a fabricated score |
| Adversarial | PASS | 5 tests |
| Mutation | PASS | 2 tests |
| Chaos | PASS | 2 tests |
| Performance / scale | PASS | 1,000 candidates and 10,000 results aggregated in 21.9ms |
| Shadow, canary, promotion, rollback, drift | PASS | 9 tests |
| Optimization runtime and store | PASS | 5 tests |
| CLI / Web / MCP / A2A / TUI conformance | PASS | 12 surface tests; every Process 08 surface is read-only |
| Full P03→P08 lifecycle E2E | PASS | `TestProcess03Through08Lifecycle` |
| Pack and result integrity | PASS | `tools/release_verify.py` PASS, `validate-pack` PASS |
| Python suites | PASS | 27 tests |

## Artifact digests

The manifest digest below is the value verified on exact main
`a1de3f70144a51670bab02f1384e28f9d8dcb151`, before this attestation and the
refreshed qualification record were added:

```
910de89859b6b58d0bebc1351c8b50be2d29a87546ff7c4cacd29bea8489ae76  distribution/PACK-MANIFEST.json
```

The manifest necessarily changes when these two documents are registered in it,
so it cannot record its own post-commit digest. Integrity of the updated
manifest is verified by `tools/release_verify.py`, which passes on the commit
carrying this attestation.

## Promotion and rollback state

No optimization candidate is promoted in production configuration by this work.
Promotion, canary and rollback are exercised through tests and the lifecycle
E2E, which ends in a bounded canary that is rolled back with its reason
preserved. There is no live canary and no outstanding rollback.

## Unresolved gaps

These remain `NOT_RUN`. They are not inferred from adjacent evidence, and none
of them is upgraded on the strength of the adapters that would consume them.

- **Terminal-Bench**: no Harbor or `tb` binary is installed on this host, so no
  benchmark task was executed. The adapter exists and its tests prove it reports
  `NOT_RUN` per pinned task rather than fabricating a score.
- **Official SWE-bench Verified**: the official evaluator and the `swebench`
  package are not installed. Docker is running and the network is reachable, so
  the blocker is the absent harness rather than the environment. Only an
  official-harness verdict would count as a resolution.
- **Real governed provider execution**: no external provider credentials were
  used, so no counterfactual or routing observation rests on a real paid
  provider run. This carries forward from Process 06 and Process 07.
- **Full chaos and fault matrix**: the core chaos cases pass. The complete
  disk-full, network-partition and evaluator-timeout matrix in the pack has not
  been executed end to end.

## Identity

Every commit from the Process 07 base to this main is authored and committed by
Zen1th53 &lt;extreme29@proton.me&gt;. No AI attribution appears in any commit
message, trailer, branch or tag.

## Final status

Every required functional gate passes on exact main. The Process 07
counterfactual carry-forward is closed by real sandboxed execution. The
remaining `NOT_RUN` rows are environmental — two absent external benchmark
harnesses, absent provider credentials, and an incomplete fault matrix — and are
recorded as such rather than upgraded.

`NOT_RUN` is never upgraded to PASS.
